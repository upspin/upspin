package main

import (
	"crypto/sha1"
	"fmt"
	"log"
	"os"
	filepath "path"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	xcontext "golang.org/x/net/context"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/pack"
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
	lastId     fuse.NodeID     // The last node ID assigned to a file.  A new ID is created
	// for each file as it is seen and roughly corresponds to a POSIX inode number.
	cacheDir string              // Directory for in-the-clear cached files.
	userDirs map[string]struct{} // A set of user directories we know to exist in the root.
	// This is used for reads/lookups of the root directory.
}

type nodeType uint8

const (
	rootNode nodeType = iota // There is only one root.
	userNode                 // All nodes directly below the root represent user directories.
	otherNode
)

// node represents a node (directory of file) in the namespace tree.  All nodes
// under the root are user directories.
type node struct {
	sync.Mutex // Protects concurrent access to the rest of this struct.
	t          nodeType
	id         fuse.NodeID     // See the explanation on upspinFS.
	f          *upspinFS       // File system this node belongs to.
	uname      upspin.PathName // The complete upspin path name of the node.
	user       upspin.UserName // The upspin user whose directory tree contains this node.
	attr       fuse.Attr       // Attributes of this node, e.g. POSIX mode bits.
}

// handle represents an open file.
type handle struct {
	n     *node              // Associated node.
	file  *os.File           // A open file that the in the clear content is stored in.
	fname string             // The name of the open file.
	dirty bool               // Write back on last close.
	de    []*upspin.DirEntry // If this is a directory, its contents.
}

// newUpspinFS creates a new upspin file system.
func newUpspinFS(context *upspin.Context, users *userCache) *upspinFS {
	f := &upspinFS{
		context:  context,
		users:    users,
		uid:      os.Getuid(),
		gid:      os.Getgid(),
		userDirs: make(map[string]struct{}),
	}
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	f.cacheDir = homeDir + "/upspin/cache"
	os.Mkdir(f.cacheDir, 0700)
	f.root = f.allocNode(nil, 0500|os.ModeDir, "")
	f.allocNode(f.root, 0700|os.ModeDir, string(f.context.UserName))
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

func (f *upspinFS) allocNode(parent *node, mode os.FileMode, name string) *node {
	n := &node{id: f.allocNodeId(), f: f}
	now := time.Now()
	n.attr = fuse.Attr{
		Mode:   mode,
		Atime:  now,
		Ctime:  now,
		Mtime:  now,
		Crtime: now,
		Uid:    uint32(f.uid),
		Gid:    uint32(f.gid),
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
		}
	}
	return n
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
	nn := f.allocNode(n, req.Mode&0777, req.Name)
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	h := &handle{n: nn, dirty: true, fname: filepath.Join(n.f.cacheDir, fmt.Sprintf("new.%d", n.id))}
	var err error
	h.file, err = os.Create(h.fname)
	if err != nil {
		return nil, nil, eio("%s creating  %q file %q", err, nn.uname, h.fname)
	}
	// Make sure we can actually create this node.
	// TODO(p): this creates trash objects in a immutable store. Must be a better way.
	if err := nn.put([]byte{0}); err != nil {
		// TODO(p): clear user cache and retry?
		h.file.Close()
		os.Remove(h.fname)
		return nil, nil, eio("%s Directory.Put %q", err, nn.uname)
	}
	resp.Node = h.n.id
	resp.Attr = nn.attr
	resp.EntryValid = time.Hour // TODO(p): figure out what would be right.
	return nn, h, nil
}

// Mkdir implements fs.NodeMkdirer.Mkdir.
// Creates a directory without opening it.
func (n *node) Mkdir(context xcontext.Context, req *fuse.MkdirRequest) (fs.Node, error) {
	log.Printf("Mkdir %q in %q", req.Name, n.uname)
	now := time.Now()
	nn := n.f.allocNode(n, (req.Mode&0777)|os.ModeDir, req.Name)
	nn.attr = fuse.Attr{
		Mode:   req.Mode | os.ModeDir,
		Atime:  now,
		Ctime:  now,
		Mtime:  now,
		Crtime: now,
		Uid:    req.Header.Uid,
		Gid:    req.Header.Gid,
	}
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
		return &handle{n: n, de: de}, nil
	}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return nil, enoent("%s looking up user %q", err, n.user)
	}
	pattern := path.Join(n.uname, "*")
	de, err := ue.dir.Glob(string(pattern))
	if err != nil {
		return nil, eio("%s globing %q", err, pattern)
	}
	return &handle{n: n, de: de}, nil
}

// openFile opens the file and reads its contents.  If the file is not plain text, we will reuse the cached version of the file.
func (n *node) openFile(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	log.Printf("openFile %q", n.uname)
	if n.attr.Mode&os.ModeDir != 0 {
		return nil, eisdir("%q", n.uname)
	}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return nil, enoent("%q", n.user)
	}
	de, err := ue.dir.Lookup(n.uname)
	if err != nil {
		return nil, enoent("%q", n.uname)
	}

	var finalErr error
	locations := []upspin.Location{de.Location}
	h := &handle{n: n}
	for i := 0; i < len(locations); i++ {
		loc := locations[i]
		store := n.f.context.Store
		if loc.Endpoint != store.Endpoint() {
			var err error
			store, err = bind.Store(n.f.context, loc.Endpoint)
			if err != nil {
				finalErr = eio("%s bind.Store %v", err, loc)
				continue
			}
		}
		h.fname = filepath.Join(n.f.cacheDir, fingerprint(loc))
		if loc.Reference.Packing != upspin.PlainPack {
			h.file, err = os.OpenFile(h.fname, int(req.Flags), 0700)
			if err == nil {
				return h, nil
			}
		}
		var data []byte
		var locs []upspin.Location
		if data, locs, err = store.Get(loc.Reference.Key); err != nil {
			finalErr = eio("%s Get %q key %q file %q", err, h.n.uname, loc.Reference.Key, h.fname)
			continue
		}
		if len(locs) > 0 {
			locations = append(locations, locs...)
			continue
		}
		packer := pack.Lookup(loc.Reference.Packing)
		if packer == nil {
			finalErr = eio("couldn't lookup %q key %q file %q", h.n.uname, loc.Reference.Key, h.fname)
			continue
		}
		clearLen := packer.UnpackLen(n.f.context, data, &de.Metadata)
		if clearLen < 0 {
			finalErr = eio("couldn't unpack %q key %q file %q", h.n.uname, loc.Reference.Key, h.fname)
			continue
		}
		cleartext := make([]byte, clearLen)
		len, err := packer.Unpack(n.f.context, cleartext, data, &de.Metadata, h.n.uname)
		if err != nil {
			finalErr = eio("%s unpacking %q key %q file %q", err, h.n.uname, loc.Reference.Key, h.fname)
			continue
		}
		cleartext = cleartext[:len]
		// Save a copy o the cleartext in the local file system.
		if h.file, err = os.Create(h.fname); err != nil {
			return nil, eio("%s creating %q key %q file %q", err, h.n.uname, loc.Reference.Key, h.fname)
		}
		if wlen, err := h.file.Write(cleartext); err != nil || len != wlen {
			return nil, eio("%s writing %q key %q file %q", err, h.n.uname, loc.Reference.Key, h.fname)
		}
		n.Lock()
		n.attr.Size = uint64(len)
		n.Unlock()
		return h, nil
	}
	return nil, finalErr
}

func fingerprint(loc upspin.Location) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(loc.Reference.Key)))
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
	ue, err := n.f.users.lookup(n.user)
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
	nn := n.f.allocNode(n, mode, name)

	// If this is the root, add an entry for this user directory so ReadDirAll will work.
	if n.t == rootNode {
		n.f.addUserDir(name)
	}
	return nn, nil
}

func (f *upspinFS) addUserDir(name string) {
	f.Lock()
	if _, ok := f.userDirs[name]; !ok {
		f.userDirs[name] = struct{}{}
	}
	f.Unlock()
}

// Setattr implements fs.NodeSetattrer.Setattr.
func (n *node) Setattr(context xcontext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	log.Printf("Setattr %q", n.uname)
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
	log.Printf("Read %q %d bytes at %d", h.n.uname, len(resp.Data), req.Offset)
	n, err := h.file.ReadAt(resp.Data, req.Offset)
	if err == nil {
		return err
	}
	if n != len(resp.Data) {
		resp.Data = resp.Data[:n]
	}
	return err
}

// Write implements fs.HandleWriter.Write.
func (h *handle) Write(context xcontext.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	log.Printf("Write %q %d bytes at %d", h.n.uname, len(req.Data), req.Offset)
	n, err := h.file.WriteAt(req.Data, req.Offset)
	if err != nil {
		return eio("%s writing to %q file %q", err, h.n.uname, h.fname)
	}
	resp.Size = n
	newSize := uint64(req.Offset) + uint64(len(req.Data))
	if newSize > h.n.attr.Size {
		h.n.Lock()
		h.n.attr.Size = newSize
		h.n.Unlock()
	}
	return nil
}

// Release implements fs.HandleWriter.Release.  This corresponds to a user close and, if the file is dirty,
// should be written back to the store.
// TODO(p): If we fail writing a file, should we try later asynchronously?
func (h *handle) Release(context xcontext.Context, req *fuse.ReleaseRequest) error {
	log.Printf("Release %q", h.n.uname)
	if !h.dirty {
		h.file.Close()
		return nil
	}
	info, err := h.file.Stat()
	if err != nil {
		return eio("%s stating %s (%q)", err, h.fname, h.n.uname)
	}
	cleartext := make([]byte, info.Size())
	var sofar int64
	for sofar != info.Size() {
		n, err := h.file.ReadAt(cleartext[sofar:], sofar)
		if err != nil {
			return eio("%s reading %s (%q)", err, h.fname, h.n.uname)
		}
		sofar += int64(n)
	}
	// Create the directory entry.
	if err = h.n.put(cleartext); err != nil {
		return eio("%s Directory.Put(%s)", err, h.n.uname)
	}
	return nil
}

func (n *node) put(cleartext []byte) error {
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return eio("%s looking up %s", err, n.user)
	}
	packer := pack.Lookup(n.f.context.Packing)
	if packer == nil {
		return eio("unrecognized Packing %d for %q", n.f.context.Packing, n.uname)
	}
	meta := &upspin.Metadata{}
	// Get a buffer big enough for this data.
	cipherLen := packer.PackLen(n.f.context, cleartext, meta, n.uname)
	if cipherLen < 0 {
		return eio("PackLen failed for %q", n.uname)
	}
	// TODO: Some packers don't update the meta in PackLen, but some do. If not done, update it now.
	if len(meta.PackData) == 0 {
		meta.PackData = make(upspin.PackData, 1)
		meta.PackData[0] = byte(n.f.context.Packing)
	}
	cipher := make([]byte, cipherLen)
	len, err := packer.Pack(n.f.context, cipher, cleartext, meta, n.uname)
	if err != nil {
		return eio("%s Pack(%s)", err, n.uname)
	}
	cipher = cipher[:len]
	// Create the directory entry.
	_, err = ue.dir.Put(n.uname, cipher, meta.PackData)
	if err != nil {
		return eio("%s Directory.Put(%s)", err, n.uname)
	}
	return nil
}
