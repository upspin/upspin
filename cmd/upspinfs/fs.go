// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

package main

import (
	"fmt"
	"io"
	"os"
	ospath "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gContext "golang.org/x/net/context"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/shutdown"
	"upspin.io/upspin"
	"upspin.io/user"
)

const (
	// defaultValid is how long the kernel can cache
	// attribute information that upspinfs gives it.
	defaultValid = 1 * time.Minute

	// Files and directories will appear in the host OS with
	// these permissions, regardless of Access file contents.
	// TODO(p): consider reflecting the actual Access file
	// permissions.
	unixPermissions = 0700

	// enoentValidInterval is the interval after which we stop
	// remembering the non existence of files for files not being
	// watched.
	enoentValidInterval = time.Minute
)

// upspinFS represents an instance of the mounted file system.
type upspinFS struct {
	sync.Mutex                               // Protects concurrent access to the rest of this struct.
	mountpoint string                        // Absolute Unix path to mountpoint.
	config     upspin.Config                 // Upspin config used for all requests.
	client     upspin.Client                 // A client to use for client methods.
	root       *node                         // The root of the Upspin file system.
	uid        int                           // OS user id of this process' owner.
	gid        int                           // OS group id of this process' owner.
	lastID     fuse.NodeID                   // The last node ID created and assigned to a file.
	userDirs   map[string]bool               // Set of known user directories.
	cache      *cache                        // A cache of files read from or to be written to dir/store.
	nodeMap    map[upspin.PathName]*node     // All in use nodes.
	enoentMap  map[upspin.PathName]time.Time // A map of non-existent names.
	server     *fs.Server                    // The Bazil server interface.
	watched    *watchedRoots                 // Directory servers being watched.
}

type nodeType uint8

const (
	rootNode nodeType = iota // There is only one root.
	userNode                 // All nodes directly below the root represent user directories.
	otherNode
)

// node represents a node (directory or file) in the name space tree.  All nodes
// under the root are user directories.
type node struct {
	sync.Mutex // Protects concurrent access to the rest of this struct.
	t          nodeType
	id         fuse.NodeID      // See the explanation on upspinFS.
	f          *upspinFS        // File system this node belongs to.
	uname      upspin.PathName  // The complete Upspin path name of the node.
	user       upspin.UserName  // The Upspin user whose directory tree contains this node.
	attr       fuse.Attr        // Attributes of this node, e.g. POSIX mode bits.
	handles    map[*handle]bool // Handles (open instances) of this node.
	link       upspin.PathName  // If this is a symlink, the target.
	noWB       bool             // Don't write back if set.
	deleted    bool             // A watch event deleted this node.

	// cached info.
	cf  *cachedFile        // Local file system contents of this node.
	de  []*upspin.DirEntry // Directory contents of this node.
	seq int64              // Seq when last opened.

	refreshTime  time.Time // The next refresh of the node info.
	doNotRefresh bool      // True if we should not try refreshing.
}

func (n *node) String() string {
	return fmt.Sprintf("%s %#x", n.uname, uint64(n.id))
}

// handle represents an open file.
type handle struct {
	n     *node          // Associated node.
	flags fuse.OpenFlags // flags used to  open the file.
	id    int
}

func (h *handle) String() string {
	return fmt.Sprintf("%s %#x", h.n, h.id)
}

// newUpspinFS creates a new Upspin file system.
func newUpspinFS(config upspin.Config, mountpoint string, cacheDir string, cacheSize int64) *upspinFS {
	sep := string(filepath.Separator)
	if !strings.HasSuffix(mountpoint, sep) {
		mountpoint = mountpoint + sep
	}
	f := &upspinFS{
		mountpoint: mountpoint,
		config:     config,
		client:     client.New(config),
		uid:        os.Getuid(),
		gid:        os.Getgid(),
		userDirs:   make(map[string]bool),
		nodeMap:    make(map[upspin.PathName]*node),
		enoentMap:  make(map[upspin.PathName]time.Time),
	}
	f.cache = newCache(config, cacheDir+"/fscache", cacheSize)
	f.watched = newWatchedDirs(f)

	// Preallocate root node.
	f.root = f.allocNode(nil, "", 0500|os.ModeDir, 0, time.Now())
	return f
}

// All capitailized *upspinFS, *node, and *handle methods represent the interface
// to fuse/fs.

// Root implements fs.Root.  It returns the root node of the file system.
func (f *upspinFS) Root() (fs.Node, error) {
	return f.root, nil
}

// Statfs implements fs.Statfser.  We make up the response just to keep FUSE happy.
func (f *upspinFS) Statfs(ctx gContext.Context, req *fuse.StatfsRequest, resp *fuse.StatfsResponse) error {
	resp.Blocks = 1000000000 // Total data blocks in file system.
	resp.Bfree = 1000000000  // Free blocks in file system.
	resp.Bavail = 1000000000 // Free blocks in file system if you're not root.
	resp.Files = 100000      // Total files in file system.
	resp.Ffree = 100000      // Free files in file system.
	resp.Bsize = 64 * 1024   // Block size
	resp.Namelen = 256       // Maximum file name length?
	resp.Frsize = 1          // Fragment size, smallest addressable data size in the file system.
	return nil
}

func (f *upspinFS) allocNode(parent *node, name string, mode os.FileMode, size uint64, mtime time.Time) *node {
	n := &node{f: f}
	now := time.Now()
	n.attr = fuse.Attr{
		Valid:     defaultValid,
		Mode:      mode,
		Atime:     now,
		Ctime:     mtime,
		Mtime:     mtime,
		Crtime:    mtime,
		Uid:       uint32(f.uid),
		Gid:       uint32(f.gid),
		Size:      size,
		Blocks:    (size + 511) / 512,
		BlockSize: 4096,
		Nlink:     1,
	}
	if parent == nil {
		n.t = rootNode
	} else {
		n.uname = path.Join(parent.uname, name)
		switch parent.t {
		case rootNode:
			n.user = upspin.UserName(name)
			n.t = userNode
		default:
			n.user = parent.user
			n.t = otherNode
			n.attr.Size = size
		}
	}
	n.handles = make(map[*handle]bool)
	f.Lock()
	f.lastID++
	n.id = f.lastID
	f.Unlock()
	n.attr.Inode = uint64(n.id)
	n.refreshTime = now.Add(refreshInterval)
	return n
}

// dirLookup returns a bound directory for user 'name'.
func (f *upspinFS) dirLookup(name upspin.UserName) (upspin.DirServer, error) {
	return bind.DirServerFor(f.config, name)
}

var handleID int
var hl sync.Mutex

// allocHandle is called with n locked.
func allocHandle(n *node) *handle {
	h := &handle{n: n}
	n.handles[h] = true
	hl.Lock()
	h.id = handleID
	handleID++
	hl.Unlock()
	return h
}

func (h *handle) freeNoLock() {
	n := h.n
	delete(n.handles, h)
	if len(n.handles) == 0 {
		n.cf.close()
		n.cf = nil
	}
}

// Attr implements fs.Node.Attr.
func (n *node) Attr(addscontext gContext.Context, attr *fuse.Attr) error {
	const op errors.Op = "Attr"
	log.Debug.Printf("Attr %s %v %s", n, n.attr, n.attr.Mtime)
	n.Lock()
	defer n.Unlock()
	if n.deleted {
		return e2e(errors.E(op, errors.NotExist, n.uname))
	}
	if err := n.f.watched.refresh(n); err != nil {
		return err
	}
	*attr = n.attr
	return nil
}

// Access implements fs.NodeAccesser.Access.
func (n *node) Access(context gContext.Context, req *fuse.AccessRequest) error {
	// Allow all access.
	const op errors.Op = "Access"
	n.Lock()
	defer n.Unlock()
	if n.deleted {
		return e2e(errors.E(op, errors.NotExist, n.uname))
	}
	return n.f.watched.refresh(n)
}

// Create implements fs.NodeCreator.Create. Creates and opens a file.
// Every created file is initially backed by a clear text local file which is
// Put in an Upspin DirServer on close.  It is assumed that 'n' is a directory.
func (n *node) Create(context gContext.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	const op errors.Op = "Create"
	n.Lock()
	defer n.Unlock()
	f := n.f
	if n.t == rootNode {
		// User directories are directly below the root.  We can't create
		// them, they are implied.
		return nil, nil, e2e(errors.E(op, errors.Permission, "can't create in root"))
	}

	// A new node.
	nn := f.allocNode(n, req.Name, unixPermissions, 0, time.Now())
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid

	// Make sure we can actually create this node.
	if err := nn.f.checkAccess(nn.uname, nn.user, access.Create); err != nil {
		return nil, nil, e2e(errors.E(op, err))
	}

	// Open it.
	nn.Lock()
	defer nn.Unlock()
	h := allocHandle(nn)
	if err := f.cache.create(h); err != nil {
		return nil, nil, e2e(errors.E(op, err))
	}
	if resp != nil {
		resp.Node = nn.id
		resp.Attr = nn.attr
		resp.EntryValid = defaultValid
	}
	nn.exists()
	return nn, h, nil
}

// Mkdir implements fs.NodeMkdirer.Mkdir.
// Creates a directory without opening it.
func (n *node) Mkdir(context gContext.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	const op errors.Op = "Mkdir"
	n.Lock()
	defer n.Unlock()

	nn := n.f.allocNode(n, req.Name, unixPermissions|os.ModeDir, 0, time.Now())
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	dir, err := n.f.dirLookup(nn.user)
	if err != nil {
		return nil, e2e(errors.E(op, err))
	}
	entry := &upspin.DirEntry{
		Name:       upspin.PathName(nn.uname),
		SignedName: upspin.PathName(nn.uname),
		Attr:       upspin.AttrDirectory,
	}
	if _, err := dir.Put(entry); err != nil {
		// TODO: implement links.
		// TODO(p): remove from directory cache and retry?
		return nil, e2e(errors.E(op, err, nn.uname))
	}
	if n.t == rootNode {
		n.f.addUserDir(req.Name)
	}
	nn.exists()
	return nn, nil
}

// Mknod implements fs.NodeMknoder.Mknod. Creates a file or a directory.
func (n *node) Mknod(context gContext.Context, req *fuse.MknodRequest) (fs.Node, error) {
	const op errors.Op = "Mknod"

	// Only allow regular files and directories.
	if req.Mode&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeCharDevice|os.ModeSymlink) != 0 {
		return nil, e2e(errors.E(op, errors.Invalid, "special nodes not supported"))
	}

	// Use Mkdir to create a directory.
	if req.Mode&os.ModeDir != 0 {
		mkdirReq := &fuse.MkdirRequest{
			Header: req.Header,
			Name:   req.Name,
		}
		return n.Mkdir(context, mkdirReq)
	}

	// Use Create to create a regular file.
	createReq := &fuse.CreateRequest{
		Header: req.Header,
		Name:   req.Name,
	}
	nn, h, err := n.Create(context, createReq, nil)
	if err != nil {
		return nn, err
	}
	h.(*handle).Release(context, nil)
	return nn, err
}

// Open implements fs.NodeOpener.Open.  Pertains to files and directories.
// For both, we read the contents on open.
func (n *node) Open(context gContext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	const op errors.Op = "Open"
	if n.deleted {
		return nil, e2e(errors.E(op, errors.NotExist, n.uname))
	}
	if req.Dir {
		return n.openDir(context, req, resp)
	}
	return n.openFile(context, req, resp)
}

// openDir opens the directory and reads its contents.
func (n *node) openDir(context gContext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	const op errors.Op = "Open"
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, e2e(errors.E(op, errors.NotDir, n.uname))
	}
	if n.t == rootNode {
		// The root is a special case since it is a local fiction.
		n.f.Lock()
		var de []*upspin.DirEntry
		for u := range n.f.userDirs {
			de = append(de, &upspin.DirEntry{Name: upspin.PathName(u), Attr: upspin.AttrDirectory})
		}
		n.f.Unlock()
		n.Lock()
		defer n.Unlock()
		h := allocHandle(n)
		h.flags = req.Flags
		n.de = de
		return h, nil
	}
	dir, err := n.f.dirLookup(n.user)
	if err != nil {
		return nil, e2e(errors.E(op, err))
	}
	de, err := dir.Glob(upspin.AllFilesGlob(n.uname))
	if err != nil {
		return nil, e2e(errors.E(op, err, n.uname))
	}
	n.Lock()
	h := allocHandle(n)
	n.de = de
	h.flags = req.Flags

	// Directory mtime is largest of its direct descendants and itself.
	for _, child := range de {
		t := child.Time.Go()
		if t.After(n.attr.Mtime) {
			n.attr.Mtime = t
		}
	}
	n.Unlock()

	// Update any known nodes.
	for _, child := range de {
		n.f.Lock()
		cn, ok := n.f.nodeMap[child.Name]
		n.f.Unlock()
		if !ok {
			continue
		}
		cn.Lock()
		if sz, err := lstatSize(child, n); err == nil {
			cn.attr.Size = sz
		} else {
			return nil, e2e(errors.E(op, err, n.uname))
		}
		cn.attr.Mtime = child.Time.Go()
		cn.Unlock()
	}
	return h, nil
}

// openFile opens the file and reads its contents.  If the file is not plain text, we will reuse the cached version of the file.
func (n *node) openFile(context gContext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	const op errors.Op = "Open"
	n.Lock()
	defer n.Unlock()
	n.f.watched.refresh(n)
	if n.attr.Mode&os.ModeDir != 0 {
		return nil, e2e(errors.E(op, errors.IsDir, n.uname))
	}

	// Make sure we can actually write this node if requested.
	if req.Flags.IsWriteOnly() || req.Flags.IsReadWrite() {
		if err := n.f.checkAccess(n.uname, n.user, access.Write); err != nil {
			return nil, e2e(errors.E(op, err))
		}
	}

	h := allocHandle(n)
	err := n.f.cache.open(h, req.Flags)
	if err != nil {
		return nil, e2e(errors.E(op, err, n.uname))
	}
	return h, nil
}

// directoryLookup returns the DirServer and DirEntry for uname.
// n represents uname's parent directory.
func (n *node) directoryLookup(uname upspin.PathName) (upspin.DirServer, *upspin.DirEntry, error) {
	// Check the file mode to make sure FUSE isn't asking to
	// perform directory relative operations to a non
	// directory. Versions have in the past.
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, nil, errors.E(errors.NotDir, n.uname)
	}
	return n.lookup(uname)
}

// lookup returns the DirServer and DirEntry for uname.
// n represents uname's parent directory or uname itself.
func (n *node) lookup(uname upspin.PathName) (upspin.DirServer, *upspin.DirEntry, error) {
	f := n.f
	if f.isEnoent(uname) {
		return nil, nil, errors.E(errors.NotExist)
	}
	user := n.user
	if n.t == rootNode {
		if parsed, err := path.Parse(uname); err != nil {
			// If the name doesn't parse, just treat it
			// as a nonexistent name.
			return nil, nil, errors.E(errors.NotExist)
		} else {
			user = parsed.User()
		}
	}
	dir, err := n.f.dirLookup(user)
	if err != nil {
		return nil, nil, err
	}
	de, err := dir.Lookup(uname)
	if err != nil {
		if err == upspin.ErrFollowLink {
			// Since FUSE walks names a step at a time we shouldn't accidentally
			// pass a link without noticing but this ensures it.
			if de.Name == uname {
				return dir, de, nil
			}
			f.doesNotExist(uname)
			return nil, nil, err
		}
		kind := classify(err)
		if kind == errors.Private {
			// We act like the error didn't happen in the hopes that
			// a later longer path will succeed.
			de = &upspin.DirEntry{Name: uname, Attr: upspin.AttrDirectory}
			return dir, de, nil
		}
		f.doesNotExist(uname)
		return nil, nil, err
	}
	return dir, de, nil
}

// Remove implements fs.NodeRemover.  'n' is the directory in which the file
// req.Name resides.  req.Dir flags this as an rmdir.
func (n *node) Remove(context gContext.Context, req *fuse.RemoveRequest) error {
	const op errors.Op = "Remove"
	n.Lock()
	defer n.Unlock()

	uname := path.Join(n.uname, req.Name)

	// Find the node in question.
	dir, de, err := n.directoryLookup(uname)
	if err != nil {
		return e2e(errors.E(op, uname, err))
	}

	// Make sure the requested type (directory or not) matches.
	if req.Dir {
		if !de.IsDir() {
			return e2e(errors.E(op, errors.NotDir, uname))
		}
	} else {
		if de.IsDir() {
			return e2e(errors.E(op, errors.IsDir, uname))
		}
	}

	// Delete from the directory (but not the store).
	_, err = dir.Delete(uname)
	if err != nil {
		// TODO: implement links.
		return e2e(errors.E(op, uname, err))
	}

	// Fix the node maps.
	fn := n.f.doesNotExist(uname)

	// Avoid write back if the file is currently in use.
	if fn != nil {
		fn.Lock()
		fn.noWB = true
		fn.Unlock()
	}

	// Forget the directory entry.
	for i, de := range n.de {
		if uname == de.Name {
			n.de = append(n.de[0:i], n.de[i+1:]...)
		}
	}

	return nil
}

// lstatSize returns a lstat-compatible size for the dir entry.  The size only
// differs for symlinks.  Upspin's DirEntry size for a link is zero, but for
// lstat, the size of the link is the size of the link content.
func lstatSize(de *upspin.DirEntry, n *node) (uint64, error) {
	if !de.IsLink() {
		s, err := de.Size()
		return uint64(s), err
	}
	// It seems that upspin treats all symlinks that don't leave the filesystem
	// as relative, so replicate that approach here.  This may have interesting
	// side effects for programs that care about precise link content, like
	// version control systems.
	p, err := n.upspinPathToHostPath(de.Link)
	if err != nil {
		return 0, err
	}
	return uint64(len(p)), nil
}

// Lookup implements fs.NodeStringLookuper.Lookup. 'n' must be a directory.
// We do not use cached knowledge of 'n's contents.
func (n *node) Lookup(context gContext.Context, name string) (fs.Node, error) {
	const op errors.Op = "Lookup"
	n.Lock()
	uname := path.Join(n.uname, name)
	n.Unlock()

	f := n.f
	f.Lock()
	if n, ok := f.nodeMap[uname]; ok {
		f.Unlock()
		n.Lock()
		defer n.Unlock()
		if err := f.watched.refresh(n); err != nil {
			return nil, err
		}
		return n, nil
	}
	f.Unlock()

	// Hack to avoid bothering the keyserver. Extended attributes for
	// file "<name>" is implemented as an Upspin file named "._<name>".
	// Because a user's root is represented as a file, this often
	// results in lookups of "._<user name>" . We short circuit these
	// requests here. Hopefully no valid user name starts with "._".
	if strings.HasPrefix(string(uname), "._") {
		return nil, e2e(errors.E(op, errors.NotExist, uname))
	}

	n.Lock()
	defer n.Unlock()

	// Ask the Dirserver.
	_, de, err := n.directoryLookup(uname)
	if err != nil {
		f.removeMapping(uname)
		return nil, e2e(errors.E(op, uname, err))
	}

	// Make a node to hand back to fuse.
	mode := os.FileMode(unixPermissions)
	if de.IsDir() {
		mode |= os.ModeDir
	}
	if de.IsLink() {
		mode |= os.ModeSymlink
	}
	size, err := lstatSize(de, n)
	if err != nil {
		f.removeMapping(uname)
		return nil, e2e(errors.E(op, n.uname, err))
	}
	nn := n.f.allocNode(n, name, mode, size, de.Time.Go())
	if de.IsLink() {
		nn.link = upspin.PathName(de.Link)
	}

	// If this is the root, add an entry for this user directory so ReadDirAll will work.
	if n.t == rootNode {
		n.f.addUserDir(name)
	}
	nn.exists()
	return nn, nil
}

func (f *upspinFS) addUserDir(name string) {
	f.Lock()
	f.userDirs[name] = true
	f.Unlock()
}

// exists remembers that a node is associated with a name.
// We assume parent node is locked.
func (n *node) exists() {
	f := n.f
	f.Lock()
	n.deleted = false
	_, ok := f.nodeMap[n.uname]
	delete(f.enoentMap, n.uname)
	f.nodeMap[n.uname] = n
	if !ok {
		f.watched.add(n.uname)
	}
	f.Unlock()
}

// doesNotExist removes the pathname to node mapping and
// remembers that the file doesn't exist. It returns
// the old node if there was a mapping, nil otherwise.
func (f *upspinFS) doesNotExist(uname upspin.PathName) *node {
	f.Lock()
	f.enoentMap[uname] = f.enoentInvalidTime(uname)
	fn, ok := f.nodeMap[uname]
	if ok {
		delete(f.nodeMap, uname)
		f.watched.remove(uname)
	}
	f.Unlock()
	return fn
}

// enoentInvalidTime is the time an enoentMap entry becomes invalid.
func (f *upspinFS) enoentInvalidTime(uname upspin.PathName) time.Time {
	if f.watched.watchSupported(uname) {
		return time.Now().Add(1000 * time.Hour)
	}
	return time.Now().Add(enoentValidInterval)
}

// removeMapping removes the pathname to node mapping.
func (f *upspinFS) removeMapping(uname upspin.PathName) {
	f.Lock()
	_, ok := f.nodeMap[uname]
	if ok {
		delete(f.nodeMap, uname)
		f.watched.remove(uname)
	}
	f.Unlock()
}

// Forget implements  fs.Forgetter.Forget.
func (n *node) Forget() {
	n.f.removeMapping(n.uname)
}

// Setattr implements fs.NodeSetattrer.Setattr.
//
// Files are only truncated by Setattr calls.
func (n *node) Setattr(context gContext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	const op errors.Op = "Setattr"
	if req.Valid.Size() {
		// Truncate.  Lots of cases:
		// 1) we have it opened. Truncate the cached file and
		//    mark it as dirty. It will be written back when the handle is
		//    released.
		// 3) we don't have it opened and are truncating to 0. Treat this like
		//    a create.
		// 4) we don't have it opened and are truncating to non 0.  Read into
		//    a cached file, truncate, and write back to dir/store.
		n.Lock()
		if len(n.handles) > 0 {
			if err := n.cf.truncate(n, int64(req.Size)); err != nil {
				n.Unlock()
				return e2e(errors.E(op, n.uname, err))
			}
			n.Unlock()
		} else if req.Size == 0 {
			h := allocHandle(n)
			if err := n.f.cache.create(h); err != nil {
				h.freeNoLock()
				n.Unlock()
				return e2e(errors.E(op, err))
			}
			n.Unlock()
			h.Release(context, nil)
		} else {
			h := allocHandle(n)
			if err := n.f.cache.open(h, fuse.OpenReadWrite); err != nil {
				h.freeNoLock()
				n.Unlock()
				return e2e(errors.E(op, n.uname, err))
			}
			if err := n.cf.truncate(n, int64(req.Size)); err != nil {
				h.freeNoLock()
				n.Unlock()
				return e2e(errors.E(op, n.uname, err))
			}
			n.Unlock()
			h.Release(context, nil)
		}
		n.attr.Size = req.Size
	}
	if req.Valid.Mtime() {
		n.Lock()
		defer n.Unlock()

		// Upspin doesn't allow rewriting directory DirEntries.
		if n.attr.Mode&os.ModeDir == os.ModeDir {
			return nil
		}

		// Write back the file to create it.
		if err := n.cf.writeback(n); err != nil {
			return e2e(errors.E(op, n.uname, err))
		}

		// Set the time.
		if err := n.f.client.SetTime(n.uname, upspin.TimeFromGo(req.Mtime)); err != nil {
			return e2e(errors.E(op, err))
		}
		n.attr.Mtime = req.Mtime
	}
	// Ignore mode changes.
	return nil
}

// Flush implements fs.HandleFlusher.Flush.  Called when a file is closed or synced.
func (h *handle) Flush(context gContext.Context, req *fuse.FlushRequest) error {
	const op errors.Op = "Flush"

	// Write back to upspin.
	h.n.Lock()
	defer h.n.Unlock()
	if err := h.n.cf.writeback(h.n); err != nil {
		return e2e(errors.E(op, h.n.uname, err))
	}

	return nil
}

// ReadDirAll implements fs.HandleReadDirAller.ReadDirAll.
func (h *handle) ReadDirAll(context gContext.Context) ([]fuse.Dirent, error) {
	h.n.Lock()
	defer h.n.Unlock()
	var fde []fuse.Dirent
	for _, de := range h.n.de {
		parsed, _ := path.Parse(de.Name)
		name := string(de.Name)
		if !parsed.IsRoot() {
			name = parsed.Elem(parsed.NElem() - 1)
		}
		fde = append(fde, fuse.Dirent{Name: name})
	}
	return fde, nil
}

// Read implements fs.HandleReader.Read.
func (h *handle) Read(context gContext.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	const op errors.Op = "Read"
	h.n.Lock()
	defer h.n.Unlock()
	resp.Data = make([]byte, cap(resp.Data))
	n, err := h.n.cf.readAt(resp.Data, req.Offset)
	if n != len(resp.Data) {
		resp.Data = resp.Data[:n]
	}
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		err = e2e(errors.E(op, h.n.uname, err))
	}
	return err
}

// Write implements fs.HandleWriter.Write.  We lock the node for the extent of the write to serialize
// changes to the node.
func (h *handle) Write(context gContext.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	const op errors.Op = "Write"
	h.n.Lock()
	defer h.n.Unlock()
	n, err := h.n.cf.writeAt(req.Data, req.Offset)
	if err != nil {
		err = e2e(errors.E(op, h.n.uname, err))
	}
	resp.Size = n
	newSize := uint64(req.Offset) + uint64(len(req.Data))
	if newSize > h.n.attr.Size {
		h.n.attr.Size = newSize
	}
	h.n.attr.Mtime = time.Now()
	return nil
}

// Release implements fs.HandleWriter.Release. Similar to Flush but only when
// a file is finally closed.
// TODO(p): If we fail writing a file, should we try later asynchronously?
func (h *handle) Release(context gContext.Context, req *fuse.ReleaseRequest) error {
	const op errors.Op = "Release"

	// Write back to upspin.
	h.n.Lock()
	defer h.n.Unlock()
	err := h.n.cf.writeback(h.n)
	if err != nil {
		err = e2e(errors.E(op, h.n.uname, err))
	}
	h.freeNoLock()
	return err
}

// Fsync implements fs.NodeFsyncer.Fsync.
func (n *node) Fsync(ctx gContext.Context, req *fuse.FsyncRequest) error {
	return nil
}

// Link implements fs.NodeLinker.Link. It creates a new node in directory n that points to the same
// reference as old.
func (n *node) Link(ctx gContext.Context, req *fuse.LinkRequest, old fs.Node) (fs.Node, error) {
	const op errors.Op = "Link"
	return nil, unsupported(errors.E(op, n.uname, errors.Str("hard link unsuported")))
}

// Rename implements fs.Renamer.Rename. It renames the old node to r.NewName in directory n.
func (n *node) Rename(ctx gContext.Context, req *fuse.RenameRequest, newDir fs.Node) error {
	const op errors.Op = "Rename"
	nn := newDir.(*node)
	f := n.f

	// Lock both dirs in fixed order to avoid deadlock. Obey lock ordering.
	if n.uname == nn.uname {
		n.Lock()
		defer n.Unlock()
	} else if n.uname < nn.uname {
		n.Lock()
		defer n.Unlock()
		nn.Lock()
		defer nn.Unlock()
	} else {
		nn.Lock()
		defer nn.Unlock()
		n.Lock()
		defer n.Unlock()
	}
	oldPath := path.Join(n.uname, req.OldName)
	newPath := path.Join(nn.uname, req.NewName)

	f.Lock()
	newn := f.nodeMap[newPath]
	oldn := f.nodeMap[oldPath]
	f.Unlock()
	if oldn != nil {
		oldn.Lock()
		defer oldn.Unlock()
	}

	// At this point we are safe from changes in the directories since the
	// directories are locked for creation and deletion of their contents.
	// We are also safe from changes to oldn.
	de, err := f.client.Rename(oldPath, newPath)
	if err != nil {
		// FUSE semantics state that a rename should
		// remove the target if it exists.
		if !errors.Is(errors.Exist, err) {
			return e2e(errors.E(op, oldPath, err))
		}
		// Remove target and try again.
		dir, _, err := n.directoryLookup(newPath)
		if err != nil {
			return e2e(errors.E(op, newPath, err))
		}
		if _, err := dir.Delete(newPath); err != nil {
			return e2e(errors.E(op, oldPath, err))
		}
		de, err = f.client.Rename(oldPath, newPath)
		if err != nil {
			return e2e(errors.E(op, oldPath, err))
		}
	}

	f.Lock()
	defer f.Unlock()
	if newn != nil {
		// An active node for newPath is still valid but lookups
		// for newPath must not find it and it cannot be written back
		// on close.
		delete(f.nodeMap, newPath)
		f.watched.remove(newPath)
		newn.noWB = true
	}
	delete(f.enoentMap, newPath)
	if oldn != nil {
		// Any active node for oldPath must now refer to newPath.
		delete(f.nodeMap, oldPath)
		f.watched.remove(oldPath)
		oldn.uname = newPath
		oldn.user = nn.user
		oldn.seq = de.Sequence
		f.nodeMap[newPath] = oldn
		f.watched.add(newPath)
	}
	return nil
}

// The following Xattr calls exist to short circuit any xattr calls.  Without them,
// the macOS kernel will constantly look for ._ files.

// Getxattr implements fs.NodeGetxattrer.Getxattr.
func (n *node) Getxattr(ctx gContext.Context, req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse) error {
	return notSupported("getxattr")
}

// Listxattr implements fs.NodeListxattrer.Listxattr.
func (n *node) Listxattr(ctx gContext.Context, req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse) error {
	return notSupported("listxattr")
}

// Setxattr implements fs.NodeSetxattrer.Setxattr.
func (n *node) Setxattr(ctx gContext.Context, req *fuse.SetxattrRequest) error {
	return notSupported("setxattr")
}

// Removexattr implements fs.NodeRemovexattrer.Removexattr.
func (n *node) Removexattr(ctx gContext.Context, req *fuse.RemovexattrRequest) error {
	return nil
}

// convertPath converts a host path separators into upspin ones.
func convertPath(path string) upspin.PathName {
	if filepath.Separator == '/' {
		return upspin.PathName(path)
	}
	return upspin.PathName(strings.Replace(path, string(filepath.Separator), "/", -1))
}

// Symlink implements fs.Symlink.
func (n *node) Symlink(ctx gContext.Context, req *fuse.SymlinkRequest) (fs.Node, error) {
	const op errors.Op = "Symlink"
	n.Lock()
	defer n.Unlock()
	target := req.Target
	if !filepath.IsAbs(target) {
		target = filepath.Join(n.f.mountpoint, string(n.uname), target)
	}
	target = filepath.Clean(target)
	// Strip off mount point.
	mountRel := strings.TrimPrefix(target, n.f.mountpoint)
	upspinPath := convertPath(mountRel)
	if target == mountRel {
		// Don't let request walk above of the mount point.
		return nil, errors.Str("symlink outside of upspin")

	}
	log.Debug.Printf("Symlink target %q", upspinPath)
	nn := n.f.allocNode(n, req.NewName, os.ModeSymlink|unixPermissions, uint64(len(upspinPath)), time.Now())
	nn.link = upspinPath
	if err := n.f.cache.putRedirect(nn, upspinPath); err != nil {
		return nil, e2e(errors.E(op, n.uname, err))
	}
	nn.exists()
	log.Debug.Printf("Symlink %q/%q to %q returns %q", n, req.NewName, req.Target, nn)
	return nn, nil
}

// upspinPathToHostPath takes an Upspin path, target, and turns it into a host path relative
// to the Upspin path, link.
func (link *node) upspinPathToHostPath(target upspin.PathName) (string, error) {
	parsedLink, err := path.Parse(link.uname)
	if err != nil {
		return "", e2e(err)
	}
	parsedTarget, err := path.Parse(target)
	if err != nil {
		return "", e2e(err)
	}

	// Create relative path.
	nl := parsedLink.NElem()
	nt := parsedTarget.NElem()
	relPath := make([]string, 0, nl+nt+1)
	for i := -1; ; i++ {
		if i >= nl || i >= nt || parsedTarget.Elem(i) != parsedLink.Elem(i) {
			for j := i; j < nl-1; j++ {
				relPath = append(relPath, "..")
			}
			for j := i; j < nt; j++ {
				relPath = append(relPath, parsedTarget.Elem(j))
			}
			break
		}
	}
	return ospath.Join(relPath...), nil
}

// Symlink implements fs.NodeReadlinker.Readlink.
func (n *node) Readlink(ctx gContext.Context, req *fuse.ReadlinkRequest) (string, error) {
	log.Debug.Printf("Readlink %q -> %q", n, n.link)
	return n.upspinPathToHostPath(n.link)
}

// isEnoent returns true if we already know this path name doesn't exist.
func (f *upspinFS) isEnoent(uname upspin.PathName) bool {
	f.Lock()
	defer f.Unlock()
	return f.enoentMap[uname].After(time.Now())
}

// debug is used by the FUSE library to output error messages.
func debug(msg interface{}) {
	log.Debug.Printf("FUSE %v", msg)
}

// do is called both by main and testing to mount a FUSE file system. It exits on failure
// and returns when the file system has been mounted and is ready for requests.
func do(cfg upspin.Config, mountpoint string, cacheDir string, cacheSize int64, allowOther bool) chan bool {
	if log.GetLevel() == "debug" {
		fuse.Debug = debug
	}

	f := newUpspinFS(cfg, mountpoint, cacheDir, cacheSize)

	opts := []fuse.MountOption{
		fuse.FSName("upspin"),
		fuse.Subtype("fs"),
		fuse.LocalVolume(),
		fuse.VolumeName(fmt.Sprintf("%s-%s", f.config.DirEndpoint().NetAddr, f.config.UserName())),
		fuse.DaemonTimeout("240"),
		//fuse.OSXDebugFuseKernel(),
		//fuse.NoAppleDouble(),
		//fuse.NoAppleXattr(),
	}
	if allowOther {
		opts = append(opts, fuse.AllowOther())
	}

	c, err := fuse.Mount(mountpoint, opts...)
	if err == fuse.ErrOSXFUSENotFound {
		log.Fatal("FUSE for macOS is not installed. See https://osxfuse.github.io/")
	}
	if err != nil {
		log.Fatalf("fuse.Mount failed: %s", err)
	}

	// Check if the mount process has an error to report.  The timer is
	// a hack that works with older versions of the OS X FUSE extension.
	// Those versions did not signal ready when the mount had finished.
	select {
	case <-c.Ready:
		if err := c.MountError; err != nil {
			log.Debug.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
	}

	shutdown.Handle(func() {
		fuse.Unmount(mountpoint)
	})

	// Serve in a go routine.
	done := make(chan bool)
	go func() {
		f.server = fs.New(c, nil)
		err := f.server.Serve(f)
		if err != nil {
			log.Debug.Fatal(err)
		}
		close(done)
	}()

	// At this point the file system is mounted.
	// Preload the user's root.
	go func(owner upspin.UserName) {
		os.Stat(ospath.Join(mountpoint, string(owner)))
		if name, suffix, domain, err := user.Parse(owner); suffix == "" && err == nil {
			snapUser := name + "+snapshot@" + domain
			os.Stat(ospath.Join(mountpoint, string(snapUser)))
		}
	}(cfg.UserName())
	return done
}

// checkAccess determines if upspinfs has access rights to a file.
// No locking needed.
func (fs *upspinFS) checkAccess(name upspin.PathName, owner upspin.UserName, right access.Right) error {
	// Read and parse the access file.
	dir, err := fs.client.DirServer(name)
	if err != nil {
		return err
	}
	whichAccess, err := dir.WhichAccess(name)
	if err != nil {
		return err
	}
	if whichAccess == nil {
		// With no access file, the owner can do anything.
		if owner == fs.config.UserName() {
			return nil
		}
		// Everyone else can do nothing.
		return errors.E(errors.Permission, name)
	}
	accessData, err := clientutil.ReadAll(fs.config, whichAccess)
	if err != nil {
		return err
	}
	acc, err := access.Parse(whichAccess.Name, accessData)
	if err != nil {
		return err
	}

	// Check the access.
	ok, err := acc.Can(fs.config.UserName(), right, name, fs.client.Get)
	if err != nil {
		return err
	}
	if !ok {
		return errors.E(errors.Permission, name)
	}
	return nil
}
