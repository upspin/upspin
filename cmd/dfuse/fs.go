package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	xcontext "golang.org/x/net/context"

	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// upspinFS represents an instance of the mounted file system.
type upspinFS struct {
	sync.Mutex                 // Protects concurrent access to the rest of this struct.
	context    *upspin.Context // Upspin context used for all requests.
	users      *userCache      // A cache of lookups in the upspin.User service.
	root       *node           // The root of the upspin file system.
	uid        int             // OS user id of this process' owner.
	gid        int             // OS group id of this process' owner.
	lastId     fuse.NodeID     // The last node ID created and assigned to a file.
	userDirs   map[string]bool // Set of known user directories.
	cache      *cache          // A cache of files read from or to be written to dir/store.
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
	cf         *cachedFile      // Local file system cached version of this node.
	handles    map[*handle]bool // Handles (open instances) of this node.
}

// handle represents an open file.
type handle struct {
	n     *node              // Associated node.
	file  *os.File           // An open file of the in the clear cached contents.
	de    []*upspin.DirEntry // If this is a directory, its contents.
	flags fuse.OpenFlags     // flags used to  open the file.
}

// newUpspinFS creates a new upspin file system.
func newUpspinFS(context *upspin.Context, users *userCache) *upspinFS {
	f := &upspinFS{
		context:  context,
		users:    users,
		uid:      os.Getuid(),
		gid:      os.Getgid(),
		userDirs: make(map[string]bool),
	}
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	f.cache = newCache(homeDir + "/upspin/cache")
	// Preallocate root node.
	f.root = f.allocNode(nil, "", 0500|os.ModeDir, 0, time.Now())
	return f
}

// All capitailized *upspinFS, *node, and *handle methods represent the interface
// to fuse/fs.

// Mkdir implements fs.Root.  It returns the root node of the file system.
func (f *upspinFS) Root() (fs.Node, error) {
	return f.root, nil
}

func (f *upspinFS) allocNodeId() fuse.NodeID {
	f.Lock()
	f.lastId++
	x := f.lastId
	f.Unlock()
	return x
}

func (f *upspinFS) allocNode(parent *node, name string, mode os.FileMode, size uint64, mtime time.Time) *node {
	n := &node{id: f.allocNodeId(), f: f}
	now := time.Now()
	n.attr = fuse.Attr{
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
	return n
}

func allocHandle(n *node) *handle {
	h := &handle{n: n}
	n.Lock()
	n.handles[h] = true
	n.Unlock()
	return h
}

func allocHandleNoLock(n *node) *handle {
	h := &handle{n: n}
	n.handles[h] = true
	return h
}

func (h *handle) free() {
	n := h.n
	n.Lock()
	delete(n.handles, h)
	n.Unlock()
	if h.file != nil {
		h.file.Close()
	}
}

func (h *handle) freeNoLock() {
	n := h.n
	delete(n.handles, h)
	if h.file != nil {
		h.file.Close()
	}
}

// Attr implements fs.Node.Attr.
func (n *node) Attr(addscontext xcontext.Context, attr *fuse.Attr) error {
	*attr = n.attr
	return nil
}

// Access implements fs.NodeAccesser.Access.
func (n *node) Access(context xcontext.Context, req *fuse.AccessRequest) error {
	// Allow all access.
	return nil
}

// Create implements fs.NodeCreator.Create. Creates and opens a file.
// Every created file is initially backed by a clear text local file which is
// Put in an upspin Directory on close.  It is assumed that 'n' is a directory.
func (n *node) Create(context xcontext.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	log.Printf("Create %q in %q", req.Name, n.uname)
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
	h := allocHandleNoLock(nn)
	if err := f.cache.create(h); err != nil {
		return nil, nil, err
	}

	// Make sure we can actually create this node.
	// TODO(p): this creates trash objects in a immutable store. Must be a better way.
	if err := nn.cf.writeBack(nn); err != nil {
		return nil, nil, err
	}

	resp.Node = nn.id
	resp.Attr = nn.attr
	resp.EntryValid = time.Hour // TODO(p): figure out what would be right.
	return nn, h, nil
}

// Mkdir implements fs.NodeMkdirer.Mkdir.
// Creates a directory without opening it.
func (n *node) Mkdir(context xcontext.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	log.Printf("Mkdir %q in %q", req.Name, n.uname)
	nn := n.f.allocNode(n, req.Name, (req.Mode&0777)|os.ModeDir, 0, time.Now())
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	ue, err := n.f.users.lookup(nn.user)
	if err != nil {
		return nil, err
	}
	if _, err := ue.dir.MakeDirectory(upspin.PathName(nn.uname)); err != nil {
		// TODO(p): remove from user cache and retry?
		return nil, eio("%s Directory.MakeDirectory %q", err, nn.uname)
	}
	if n.t == rootNode {
		n.f.addUserDir(req.Name)
	}
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
	log.Printf("openDir %q %d", n.uname, n.t)
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, enotdir("%q", n.uname)
	}
	if n.t == rootNode {
		// The root is a special case since it is a local fiction.
		n.f.Lock()
		defer n.f.Unlock()
		var de []*upspin.DirEntry
		for u := range n.f.userDirs {
			de = append(de, &upspin.DirEntry{Name: upspin.PathName(u), Metadata: upspin.Metadata{IsDir: true}})
		}
		h := allocHandle(n)
		h.flags = req.Flags
		h.de = de
		return h, nil
	}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return nil, enoent("%s looking up user %q", err, n.user)
	}
	// The ? ensures at least one letter in the base component.  For example, Glob("p@google.com/*) also
	// returns p@google.com/ which is not what we want.
	pattern := path.Join(n.uname, "?*")
	de, err := ue.dir.Glob(string(pattern))
	if err != nil {
		return nil, eio("%s globing %q", err, pattern)
	}
	h := allocHandle(n)
	h.de = de
	h.flags = req.Flags
	return h, nil
}

// openFile opens the file and reads its contents.  If the file is not plain text, we will reuse the cached version of the file.
func (n *node) openFile(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	log.Printf("openFile %q %v", n.uname, req.Flags)
	if n.attr.Mode&os.ModeDir != 0 {
		return nil, eisdir("%q", n.uname)
	}

	n.Lock()
	defer n.Unlock()
	h := allocHandleNoLock(n)
	if err := n.f.cache.open(h, req.Flags); err != nil {
		return nil, err
	}
	return h, nil
}

func fingerprint(loc upspin.Location, seq int64) string {
	return fmt.Sprintf("%x.%d", sha1.Sum([]byte(loc.Reference)), seq)
}

// Remove implements fs.Noderemover.
// TODO(p): implement Directory.Remove
func (n *node) Remove(context xcontext.Context, req *fuse.RemoveRequest) error {
	log.Printf("Remove %q", n.uname)
	return nil
}

// Lookup implements fs.NodeStringLookuper.Lookup. 'n' must be a directory.
// We do not use cached knowledge of 'n's contents.
func (n *node) Lookup(context xcontext.Context, name string) (fs.Node, error) {
	log.Printf("Lookup %q in %q", name, n.uname)
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, enotdir("%q", n.uname)
	}
	user := n.user
	if n.t == rootNode {
		user = upspin.UserName(name)
	}
	ue, err := n.f.users.lookup(user)
	if err != nil {
		return nil, enoent("%s looking for user %q", err, n.user)
	}
	de, err := ue.dir.Lookup(path.Join(n.uname, name))
	if err != nil {
		return nil, enoent("%s looking for file %q", err, n.uname)
	}
	mode := os.FileMode(0700)
	if de.Metadata.IsDir {
		mode |= os.ModeDir
	}
	nn := n.f.allocNode(n, name, mode, de.Metadata.Size, de.Metadata.Time.Go())

	// If this is the root, add an entry for this user directory so ReadDirAll will work.
	if n.t == rootNode {
		n.f.addUserDir(name)
	}
	return nn, nil
}

func (f *upspinFS) addUserDir(name string) {
	f.Lock()
	if _, ok := f.userDirs[name]; !ok {
		f.userDirs[name] = true
	}
	f.Unlock()
}

// Setattr implements fs.NodeSetattrer.Setattr.
//
// Files are only truncated by Setattr calls.
func (n *node) Setattr(context xcontext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	log.Printf("Setattr %q", n.uname)
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
			h := allocHandleNoLock(n)
			if err := n.f.cache.create(h); err != nil {
				h.freeNoLock()
				n.Unlock()
				return err
			}
			n.Unlock()
			h.Release(context, nil)
		} else {
			h := allocHandleNoLock(n)
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
	return nil
}

// Flush implements fs.HandleFlusher.Flush.  Called when a file is closed.  This is the point where we
// will write the file to the directory and store.
func (h *handle) Flush(context xcontext.Context, req *fuse.FlushRequest) error {
	log.Printf("Flush %q", h.n.uname)
	return nil
}

// ReadDirAll implements fs.HandleReadDirAller.ReadDirAll.
func (h *handle) ReadDirAll(context xcontext.Context) ([]fuse.Dirent, error) {
	log.Printf("ReadDirAll %q", h.n.uname)
	var fde []fuse.Dirent
	for _, de := range h.de {
		parsed, _ := path.Parse(de.Name)
		name := string(de.Name)
		if len(parsed.Elems) > 0 {
			name = parsed.Elems[len(parsed.Elems)-1]
		}
		fde = append(fde, fuse.Dirent{Name: name})
	}
	log.Printf("ReadDirAll %q returns %v", h.n.uname, fde)
	return fde, nil
}

// Read implements fs.HandleReader.Read.
func (h *handle) Read(context xcontext.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	log.Printf("Read %q %d bytes at %d", h.n.uname, cap(resp.Data), req.Offset)
	resp.Data = make([]byte, cap(resp.Data))
	n, err := h.file.ReadAt(resp.Data, req.Offset)
	if n != len(resp.Data) {
		resp.Data = resp.Data[:n]
	}
	if err == io.EOF {
		return nil
	}
	return err
}

// Write implements fs.HandleWriter.Write.  We lock the node for the extent of the write to serialize
// changes to the node.
func (h *handle) Write(context xcontext.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	log.Printf("Write %q %d bytes at %d", h.n.uname, len(req.Data), req.Offset)
	h.n.Lock()
	defer h.n.Unlock()
	h.n.cf.markDirty()
	n, err := h.file.WriteAt(req.Data, req.Offset)
	if err != nil {
		return eio("writing to %q file %q: %s", err, h.n.uname, h.n.cf.fname, err)
	}
	resp.Size = n
	newSize := uint64(req.Offset) + uint64(len(req.Data))
	if newSize > h.n.attr.Size {
		h.n.attr.Size = newSize
	}
	return nil
}

// Release implements fs.HandleWriter.Release.  This corresponds to a user close and, if the file is dirty,
// it is written back to the store.
// TODO(p): If we fail writing a file, should we try later asynchronously?
func (h *handle) Release(context xcontext.Context, req *fuse.ReleaseRequest) error {
	log.Printf("Release %q", h.n.uname)

	// Write back to upspin.
	h.n.Lock()
	var err error
	if h.n.cf != nil {
		err = h.n.cf.writeBack(h.n)

		// If this is the last handle, forget about the cached entry.
		if len(h.n.handles) == 0 {
			h.n.cf = nil
		}
	}
	h.n.Unlock()
	h.free()
	return err
}
