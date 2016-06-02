package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/presotto/fuse"
	"github.com/presotto/fuse/fs"
	xcontext "golang.org/x/net/context"

	"upspin.io/client"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	defaultValid          = 2 * time.Minute
	defaultEnoentDuration = time.Minute
)

// upspinFS represents an instance of the mounted file system.
type upspinFS struct {
	sync.Mutex                               // Protects concurrent access to the rest of this struct.
	context    *upspin.Context               // Upspin context used for all requests.
	client     upspin.Client                 // A client to use for client methods.
	dc         *directoryCache               // A cache of bindings to user directories.
	root       *node                         // The root of the upspin file system.
	uid        int                           // OS user id of this process' owner.
	gid        int                           // OS group id of this process' owner.
	lastID     fuse.NodeID                   // The last node ID created and assigned to a file.
	userDirs   map[string]bool               // Set of known user directories.
	cache      *cache                        // A cache of files read from or to be written to dir/store.
	nodeMap    map[upspin.PathName]*node     // All in use nodes.
	enoentMap  map[upspin.PathName]time.Time // A map of non-existent names.
}

type nodeType uint8

const (
	rootNode nodeType = iota // There is only one root.
	userNode                 // All nodes directly below the root represent user directories.
	otherNode
)

// node represents a node (directory or file) in the namespace tree.  All nodes
// under the root are user directories.
type node struct {
	sync.Mutex // Protects concurrent access to the rest of this struct.
	t          nodeType
	id         fuse.NodeID      // See the explanation on upspinFS.
	f          *upspinFS        // File system this node belongs to.
	uname      upspin.PathName  // The complete upspin path name of the node.
	user       upspin.UserName  // The upspin user whose directory tree contains this node.
	attr       fuse.Attr        // Attributes of this node, e.g. POSIX mode bits.
	handles    map[*handle]bool // Handles (open instances) of this node.

	// cached info.
	cf *cachedFile        // Local file system contents of this node.
	de []*upspin.DirEntry // Directory contents of this node.
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

// newUpspinFS creates a new upspin file system.
func newUpspinFS(context *upspin.Context, dc *directoryCache) *upspinFS {
	f := &upspinFS{
		context:   context,
		client:    client.New(context),
		dc:        dc,
		uid:       os.Getuid(),
		gid:       os.Getgid(),
		userDirs:  make(map[string]bool),
		nodeMap:   make(map[upspin.PathName]*node),
		enoentMap: make(map[upspin.PathName]time.Time),
	}
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	f.cache = newCache(context, homeDir+"/upspin/cache")
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

func (f *upspinFS) allocNode(parent *node, name string, mode os.FileMode, size uint64, mtime time.Time) *node {
	n := &node{f: f}
	now := time.Now()
	n.attr = fuse.Attr{
		Valid:  defaultValid,
		Mode:   mode,
		Atime:  now,
		Ctime:  mtime,
		Mtime:  mtime,
		Crtime: mtime,
		Uid:    uint32(f.uid),
		Gid:    uint32(f.gid),
		Size:   size,
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
	return n
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

func (h *handle) free() {
	n := h.n
	n.Lock()
	delete(n.handles, h)
	if len(n.handles) == 0 {
		n.cf.close()
		n.cf = nil
	}
	n.Unlock()
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
func (n *node) Attr(addscontext xcontext.Context, attr *fuse.Attr) error {
	log.Debug.Printf("Attr %s", n)
	*attr = n.attr
	log.Debug.Printf("Attr %s returns %v", n, attr)
	return nil
}

// Access implements fs.NodeAccesser.Access.
func (n *node) Access(context xcontext.Context, req *fuse.AccessRequest) error {
	// Allow all access.
	log.Debug.Printf("Access %q %o", n, req.Mask)
	return nil
}

// Create implements fs.NodeCreator.Create. Creates and opens a file.
// Every created file is initially backed by a clear text local file which is
// Put in an upspin Directory on close.  It is assumed that 'n' is a directory.
func (n *node) Create(context xcontext.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	n.Lock()
	defer n.Unlock()
	log.Debug.Printf("Create %q in %q", req.Name, n)
	f := n.f
	if n.t == rootNode {
		// User directories are directly below the root.  We can't create
		// them, they are implied.
		return nil, nil, eperm("can't create in root")
	}

	// A new node.
	nn := f.allocNode(n, req.Name, req.Mode&0777, 0, time.Now())
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid

	// Open it.
	nn.Lock()
	defer nn.Unlock()
	h := allocHandle(nn)
	if err := f.cache.create(h); err != nil {
		return nil, nil, err
	}

	// Make sure we can actually create this node.
	// TODO(p): this creates trash objects in a immutable store. Must be a better way.
	log.Debug.Printf("writing back %q", nn)
	if err := nn.cf.writeBack(h); err != nil {
		return nil, nil, err
	}
	log.Debug.Printf("done writing back %q", nn)

	resp.Node = nn.id
	resp.Attr = nn.attr
	resp.EntryValid = defaultValid // TODO(p): figure out what would be right.
	nn.exists()
	log.Debug.Printf("Create %q in %q returns %q", req.Name, n, h)
	return nn, h, nil
}

// Mkdir implements fs.NodeMkdirer.Mkdir.
// Creates a directory without opening it.
func (n *node) Mkdir(context xcontext.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	n.Lock()
	defer n.Unlock()
	log.Debug.Printf("Mkdir %q in %q", req.Name, n)

	nn := n.f.allocNode(n, req.Name, (req.Mode&0777)|os.ModeDir, 0, time.Now())
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	dir, err := n.f.dc.lookup(nn.user)
	if err != nil {
		return nil, err
	}
	if _, err := dir.MakeDirectory(upspin.PathName(nn.uname)); err != nil {
		// TODO(p): remove from directory cache and retry?
		return nil, eio("%s Directory.MakeDirectory %q", err, nn.uname)
	}
	if n.t == rootNode {
		n.f.addUserDir(req.Name)
	}
	nn.exists()
	log.Debug.Printf("Mkdir %q in %q returns %q", req.Name, n, nn)
	return nn, nil
}

// Open implements fs.NodeOpener.Open.  Pertains to files and directories.
// For both, we read the contents on open.
func (n *node) Open(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if req.Dir {
		return n.openDir(context, req, resp)
	}
	return n.openFile(context, req, resp)
}

// openDir opens the directory and reads its contents.
func (n *node) openDir(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	log.Debug.Printf("openDir %q", n)
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, enotdir("%q", n.uname)
	}
	if n.t == rootNode {
		// The root is a special case since it is a local fiction.
		n.f.Lock()
		var de []*upspin.DirEntry
		for u := range n.f.userDirs {
			de = append(de, &upspin.DirEntry{Name: upspin.PathName(u), Metadata: upspin.Metadata{Attr: upspin.AttrDirectory}})
		}
		n.f.Unlock()
		n.Lock()
		defer n.Unlock()
		h := allocHandle(n)
		h.flags = req.Flags
		n.de = de
		return h, nil
	}
	dir, err := n.f.dc.lookup(n.user)
	if err != nil {
		return nil, enoent("%s looking up user %q", err, n.user)
	}
	pattern := path.Join(n.uname, "*")
	de, err := dir.Glob(string(pattern))
	log.Debug.Printf("Glob returned %s", err)
	if err != nil {
		return nil, eio("%s globing %q", err, pattern)
	}
	n.Lock()
	defer n.Unlock()
	h := allocHandle(n)
	n.de = de
	h.flags = req.Flags
	log.Debug.Printf("openDir %q returns %q", n, h)
	return h, nil
}

// openFile opens the file and reads its contents.  If the file is not plain text, we will reuse the cached version of the file.
func (n *node) openFile(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	n.Lock()
	defer n.Unlock()
	log.Debug.Printf("openFile %q %v", n, req.Flags)
	if n.attr.Mode&os.ModeDir != 0 {
		return nil, eisdir("%q", n.uname)
	}

	h := allocHandle(n)
	if err := n.f.cache.open(h, req.Flags); err != nil {
		return nil, err
	}
	log.Debug.Printf("openFile %q %v returns %q", n, req.Flags, h)
	return h, nil
}

func (n *node) directoryLookup(uname upspin.PathName) (upspin.Directory, *upspin.DirEntry, error) {
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, nil, enotdir("%q", n.uname)
	}
	f := n.f
	if f.isEnoent(uname) {
		return nil, nil, enoent("lookup %q: does not exist", uname)
	}
	user := n.user
	if n.t == rootNode {
		user = upspin.UserName(uname)
	}
	dir, err := f.dc.lookup(user)
	if err != nil {
		return nil, nil, enoent("lookup user %q: %s", n.user, err)
	}
	de, err := dir.Lookup(uname)
	if err != nil {
		if expectedError(err, dirErrors) {
			return nil, nil, f.enoent(uname, "lookup %s", err)
		}
		// Any other error may imply that our directory connection is
		// stale.  Forget the cached value and try again.
		f.dc.remove(user)
		dir, err = f.dc.lookup(user)
		if err != nil {
			return nil, nil, enoent("lookup user %q: %s", n.user, err)
		}
		de, err = dir.Lookup(uname)
		if err != nil {
			return nil, nil, enoent("lookup %q: %s", uname, err)
		}
	}
	return dir, de, nil
}

// Remove implements fs.NodeRemover.  'n' is the directory in which the file
// req.Name resides.  req.Dir flags this as an rmdir.
func (n *node) Remove(context xcontext.Context, req *fuse.RemoveRequest) error {
	n.Lock()
	defer n.Unlock()
	log.Debug.Printf("Remove %q from %q", req.Name, n)

	uname := path.Join(n.uname, req.Name)

	// Find the node in question.
	dir, de, err := n.directoryLookup(uname)
	if err != nil {
		return err
	}

	// Make sure the requested type (directory or not) matches.
	if req.Dir {
		if !de.IsDir() {
			return enotdir("%q", uname)
		}
	} else {
		if de.IsDir() {
			return eisdir("%q", uname)
		}
	}

	// Delete from the directory (but not the store).
	err = dir.Delete(uname)
	if err != nil {
		return eperm("%q: %s", uname, err)
	}

	// Fix the node maps.
	f := n.f
	f.Lock()
	delete(f.nodeMap, uname)
	f.enoentMap[uname] = time.Now().Add(defaultEnoentDuration)
	f.Unlock()

	// Forget the directory entry.
	for i, de := range n.de {
		if uname == de.Name {
			n.de = append(n.de[0:i], n.de[i+1:]...)
		}
	}

	log.Debug.Printf("Remove %q from %q OK", req.Name, n)
	return nil
}

var dirErrors = []string{
	"not found",
	"not a directory",
	"no such directory entry",
	"no such user",
}

// expectedError returns true if the error is one from the given list.
// TODO(p): normalize errors so that we can avoid so many comparisons.
func expectedError(err error, expected []string) bool {
	s := err.Error()
	for _, e := range expected {
		if strings.Contains(s, e) {
			return true
		}
	}
	return false
}

// Lookup implements fs.NodeStringLookuper.Lookup. 'n' must be a directory.
// We do not use cached knowledge of 'n's contents.
func (n *node) Lookup(context xcontext.Context, name string) (fs.Node, error) {
	n.Lock()
	defer n.Unlock()
	log.Debug.Printf("Lookup %q in %q", name, n)
	uname := path.Join(n.uname, name)

	f := n.f
	f.Lock()
	if n, ok := f.nodeMap[uname]; ok {
		f.Unlock()
		return n, nil
	}
	f.Unlock()

	// Ask the Directory.
	_, de, err := n.directoryLookup(uname)
	if err != nil {
		return nil, err
	}

	// Make a node to hand back to fuse.
	mode := os.FileMode(0700)
	if de.IsDir() {
		mode |= os.ModeDir
	}
	if de.IsRedirect() {
		mode |= os.ModeSymlink
	}
	nn := n.f.allocNode(n, name, mode, de.Metadata.Size, de.Metadata.Time.Go())

	// If this is the root, add an entry for this user directory so ReadDirAll will work.
	if n.t == rootNode {
		n.f.addUserDir(name)
	}
	nn.exists()
	log.Debug.Printf("Lookup %q in %q returns %q", name, n, nn)
	return nn, nil
}

func (f *upspinFS) addUserDir(name string) {
	f.Lock()
	if _, ok := f.userDirs[name]; !ok {
		f.userDirs[name] = true
	}
	f.Unlock()
}

// Forget implements  fs.Forgetter.Forget.
// TODO(p): Figure out how to lock the parent that we don't have a referant to.
func (n *node) Forget() {
	log.Debug.Printf("Forget %q", n)

	f := n.f
	f.Lock()
	defer f.Unlock()
	nn, ok := f.nodeMap[n.uname]
	if !ok {
		log.Debug.Printf("Forget: %q already forgotten", n)
		return
	}
	if nn != n {
		log.Debug.Printf("Forget: %q is not %q", nn, n)
		return
	}
	delete(f.nodeMap, n.uname)
	delete(f.enoentMap, n.uname)
}

// Setattr implements fs.NodeSetattrer.Setattr.
//
// Files are only truncated by Setattr calls.
func (n *node) Setattr(context xcontext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	log.Debug.Printf("Setattr %q", n)
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
			n.cf.markDirty()
			if err := os.Truncate(n.cf.fname, int64(req.Size)); err != nil {
				n.Unlock()
				return eio("truncating %q file %q: %s", n.uname, n.cf.fname, err)
			}
			n.Unlock()
		} else if req.Size == 0 {
			h := allocHandle(n)
			if err := n.f.cache.create(h); err != nil {
				h.freeNoLock()
				n.Unlock()
				return err
			}
			n.Unlock()
			h.Release(context, nil)
		} else {
			h := allocHandle(n)
			if err := n.f.cache.open(h, fuse.OpenReadWrite); err != nil {
				h.freeNoLock()
				n.Unlock()
				return err
			}
			if err := os.Truncate(n.cf.fname, int64(req.Size)); err != nil {
				h.freeNoLock()
				n.Unlock()
				return eio("truncating %q file %q: %s", n.uname, n.cf.fname, err)
			}
			n.Unlock()
			h.Release(context, nil)
		}
	}
	if req.Valid.Mtime() {
		// Set the modify time.
		// TODO(p): should we actually set the modify time?
	}
	log.Debug.Printf("Setattr %q OK", n)
	return nil
}

// Flush implements fs.HandleFlusher.Flush.  Called when a file is closed.  We can't
// really do anything here because if it takes any significant time something in
// the kernel or git times out the flush and we get errors.
func (h *handle) Flush(context xcontext.Context, req *fuse.FlushRequest) error {
	log.Debug.Printf("Flush %q", h)
	return nil
}

// ReadDirAll implements fs.HandleReadDirAller.ReadDirAll.
func (h *handle) ReadDirAll(context xcontext.Context) ([]fuse.Dirent, error) {
	log.Debug.Printf("ReadDirAll %q", h)
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
	log.Debug.Printf("ReadDirAll %q returns %v", h, fde)
	return fde, nil
}

// Read implements fs.HandleReader.Read.
func (h *handle) Read(context xcontext.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	log.Debug.Printf("Read %q %d bytes at %d", h, cap(resp.Data), req.Offset)
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
	log.Debug.Printf("Read %q %d bytes at %d returns %d, %s", h, cap(resp.Data), req.Offset, n, err)
	return err
}

// Write implements fs.HandleWriter.Write.  We lock the node for the extent of the write to serialize
// changes to the node.
func (h *handle) Write(context xcontext.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	log.Debug.Printf("Write %q %d bytes at %d", h, len(req.Data), req.Offset)
	h.n.Lock()
	defer h.n.Unlock()
	n, err := h.n.cf.writeAt(req.Data, req.Offset)
	if err != nil {
		return eio("writing to %q file %q: %s", err, h.n.uname, h.n.cf.fname, err)
	}
	resp.Size = n
	newSize := uint64(req.Offset) + uint64(len(req.Data))
	if newSize > h.n.attr.Size {
		h.n.attr.Size = newSize
	}
	log.Debug.Printf("Write %q %d bytes at %d OK", h, len(req.Data), req.Offset)
	return nil
}

// Release implements fs.HandleWriter.Release.  This corresponds to a user close and, if the file is dirty,
// it is written back to the store.
// TODO(p): If we fail writing a file, should we try later asynchronously?
func (h *handle) Release(context xcontext.Context, req *fuse.ReleaseRequest) error {
	log.Debug.Printf("Release %q %v", h.n, h)

	// Write back to upspin.
	h.n.Lock()
	defer h.n.Unlock()
	var err error
	if h.n.cf != nil {
		err = h.n.cf.writeBack(h)
	}
	h.freeNoLock()
	log.Debug.Printf("Release %q -> %s", h, err)
	return err
}

// Fsync implements fs.NodeFsyncer.Fsync.
func (n *node) Fsync(ctx xcontext.Context, req *fuse.FsyncRequest) error {
	log.Debug.Printf("Fsync %q", n)
	return nil
}

// Link implements fs.NodeLinker.Link. It creates a new node in directory n that points to the same
// reference as old.
func (n *node) Link(ctx xcontext.Context, req *fuse.LinkRequest, old fs.Node) (fs.Node, error) {
	n.Lock()
	defer n.Unlock()
	oldPath := old.(*node).uname
	newPath := path.Join(n.uname, req.NewName)
	log.Debug.Printf("Link %q to %q in %q", old.(*node), n, req.NewName)
	de, err := n.f.client.Link(oldPath, newPath)
	if err != nil {
		return nil, err
	}
	nn := n.f.allocNode(n, req.NewName, 0700, de.Metadata.Size, time.Unix(int64(de.Metadata.Time), 0))
	nn.exists()
	log.Debug.Printf("Link %q to %q in %q OK", old.(*node), n, req.NewName)
	return nn, nil
}

// Rename implements fs.Renamer.Rename. It renames the old node to r.NewName in directory n.
func (n *node) Rename(ctx xcontext.Context, req *fuse.RenameRequest, old fs.Node) error {
	n.Lock()
	defer n.Unlock()
	oldPath := path.Join(old.(*node).uname, req.OldName)
	// If we still have the old node, lock it for the duration.
	f := n.f
	f.Lock()
	oldn, ok := f.nodeMap[oldPath]
	if ok {
		oldn.Lock()
		defer oldn.Unlock()
	}
	f.Unlock()
	newPath := path.Join(n.uname, req.NewName)
	err := n.f.client.Rename(oldPath, newPath)
	if err == nil {
		f.Lock()
		delete(f.nodeMap, oldPath)
		delete(f.nodeMap, newPath)
		delete(f.enoentMap, newPath)
		if oldn != nil {
			f.nodeMap[newPath] = oldn
			oldn.uname = newPath
		}
		f.Unlock()
	}
	log.Debug.Printf("Rename %q in %s to %q in %q", req.OldName, old.(*node), req.NewName, n)
	return err
}

// The following Xattr calls exist to short circuit any xattr calls.  Without them,
// the MacOS kernel will constantly look for ._ files.

// Getxattr implements fs.NodeGetxattrer.Getxattr.
func (n *node) Getxattr(ctx xcontext.Context, req *fuse.GetxattrRequest, resp *fuse.GetxattrResponse) error {
	log.Debug.Printf("Getxattr %q %v", n, req)
	return fuse.ErrNoXattr
}

// Listxattr implements fs.NodeListxattrer.Listxattr.
func (n *node) Listxattr(ctx xcontext.Context, req *fuse.ListxattrRequest, resp *fuse.ListxattrResponse) error {
	log.Debug.Printf("Listxattr %q", n)
	return nil
}

// Setxattr implements fs.NodeSetxattrer.Setxattr.
func (n *node) Setxattr(ctx xcontext.Context, req *fuse.SetxattrRequest) error {
	log.Debug.Printf("Setxattr %q", n)
	return nil
}

// Removexattr implements fs.NodeRemovexattrer.Removexattr.
func (n *node) Removexattr(ctx xcontext.Context, req *fuse.RemovexattrRequest) error {
	log.Debug.Printf("Removexattr %q", n)
	return nil
}

// Symlink implements fs.Symlink.
func (n *node) Symlink(ctx xcontext.Context, req *fuse.SymlinkRequest) (fs.Node, error) {
	n.Lock()
	defer n.Unlock()
	log.Debug.Printf("Symlink %q/%q to %q", n, req.NewName, req.Target)
	nn := n.f.allocNode(n, req.NewName, os.ModeSymlink|0700, uint64(len(req.Target)), time.Now())
	if err := n.f.cache.putRedirect(nn, req.Target); err != nil {
		return nil, err
	}
	nn.exists()
	log.Debug.Printf("Symlink %q/%q to %q returns %q", n, req.NewName, req.Target, nn)
	return nn, nil
}

// Symlink implements fs.NodeReadlinker.Readlink.
func (n *node) Readlink(ctx xcontext.Context, req *fuse.ReadlinkRequest) (string, error) {
	log.Debug.Printf("Readlink %q", n)
	h, err := n.openFile(ctx, &fuse.OpenRequest{Flags: fuse.OpenReadOnly}, nil)
	if err != nil {
		return "", err
	}
	realh := h.(*handle)
	defer realh.free()
	buf := make([]byte, n.attr.Size)
	l, err := n.cf.readAt(buf, 0)
	if err != nil {
		return "", err
	}
	if uint64(l) != n.attr.Size {
		return "", eio("short read")
	}
	log.Debug.Printf("Readlink %q -> %q", n, string(buf))
	return string(buf), nil
}

// isEnoent returns true if we already know this path name doesn't exist.
// We assume parent node is locked.
func (f *upspinFS) isEnoent(uname upspin.PathName) bool {
	f.Lock()
	defer f.Unlock()
	t, ok := f.enoentMap[uname]
	if !ok {
		return false
	}
	if time.Now().After(t) {
		delete(f.enoentMap, uname)
		return false
	}
	return true
}

// enoent remembers that a path name doesn't exist and returns the FUSE appropriate error.
// We assume parent node is locked.
func (f *upspinFS) enoent(uname upspin.PathName, format string, args ...interface{}) *upspinError {
	f.Lock()
	delete(f.nodeMap, uname)
	f.enoentMap[uname] = time.Now().Add(defaultEnoentDuration)
	f.Unlock()
	return enoent(format, args...)
}

// exists remembers that a node is associated with a name.
// We assume parent node is locked.
func (n *node) exists() {
	f := n.f
	f.Lock()
	delete(f.enoentMap, n.uname)
	f.nodeMap[n.uname] = n
	f.Unlock()
}

// delay exists for testing.  We can insert a call to it anywhere we want to fake a delay.
func delay() {
	time.Sleep(200 * time.Millisecond)
}
