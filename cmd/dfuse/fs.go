// The upspin fuse interface.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
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

// upspinFs represents an instance of the mounted file system.
type upspinFs struct {
	sync.Mutex
	context  *upspin.Context
	users    *userCache
	root     *node
	uid      int
	gid      int
	lastId   fuse.NodeID
	cacheDir string // Directory for in the clear cached files.
	writeDir string // Directory for files being written.
}

type nodeType uint8

const (
	rootNode  = nodeType(iota)
	userNode  = nodeType(iota)
	otherNode = nodeType(iota)
)

type node struct {
	sync.Mutex
	t     nodeType
	id    fuse.NodeID
	f     *upspinFs
	uname upspin.PathName
	user  upspin.UserName
	attr  fuse.Attr
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
func newUpspinFS(context *upspin.Context, users *userCache) *upspinFs {
	f := &upspinFs{
		context: context,
		users:   users,
		uid:     os.Getuid(),
		gid:     os.Getgid(),
	}
	homeDir := "/tmp"
	if u, err := user.Current(); err == nil {
		if len(u.HomeDir) != 0 {
			homeDir = u.HomeDir
		}
	}
	f.writeDir = homeDir + "/.upspin/wb"
	f.cacheDir = homeDir + "/.upspin/cache"
	f.root = f.allocNode(nil, 0500|os.ModeDir, "")
	f.allocNode(f.root, 0700|os.ModeDir, string(f.context.UserName))
	return f
}

func (f *upspinFs) Root() (fs.Node, error) {
	return f.root, nil
}

func (f *upspinFs) allocNodeId() fuse.NodeID {
	f.Lock()
	defer f.Unlock()
	f.lastId++
	return f.lastId
}

func (f *upspinFs) allocNode(parent *node, mode os.FileMode, name string) *node {
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
// written to upspin on close.  It is assumed that 'n' is a directory.
func (n *node) Create(context xcontext.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	fmt.Printf("Create %q in %q", req.Name, n.uname)
	f := n.f
	if n.t == rootNode {
		// User directories are directly below the root.  We can't create
		// them, they are implied.
		return nil, nil, eperm("can't create in root")
	}
	nn := f.allocNode(n, req.Mode&0777, req.Name)
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	h := &handle{n: nn, dirty: true, fname: filepath.Join(n.f.cacheDir, fmt.Sprintf(".%d", n.id))}
	var err error
	h.file, err = os.Create(h.fname)
	if err != nil {
		return nil, nil, eio("%s creating  %q file %q", err, nn.uname, h.fname)
	}
	ue, err := f.users.lookup(nn.user)
	if err != nil {
		return nil, nil, err
	}
	// Make sure we can actually create this node.
	if _, err := ue.dir.Put(nn.uname, nil, nil); err != nil {
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
	fmt.Printf("Mkdir %q in %q", req.Name, n.uname)
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
	fmt.Printf("openDir %q", n.uname)
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, enotdir(string(n.uname))
	}
	if n.t == rootNode {
		// The root is a special case since it potentially contains all user
		// directories.
		return &handle{n: n, de: n.f.users.knownUserDirectories()}, nil
	}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return nil, err
	}
	de, err := ue.dir.Glob("*")
	if err != nil {
		return nil, err
	}
	return &handle{n: n, de: de}, nil
}

// openFile opens the file and reads its contents.  If the file is not plain text, we will reuse the cached version of the file.
func (n *node) openFile(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	fmt.Printf("openFile %q", string(n.uname))
	if (n.attr.Mode & os.ModeDir) != 0 {
		return nil, eisdir(string(n.uname))
	}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return nil, enoent(string(n.user))
	}
	de, err := ue.dir.Lookup(n.uname)
	if err != nil {
		return nil, enoent(string(n.uname))
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
			finalErr = eio("%s Get %q key %q file %q", err, string(h.n.uname), loc.Reference.Key, h.fname)
			continue
		}
		if len(locs) > 0 {
			locations = append(locations, locs...)
			continue
		}
		packer := pack.Lookup(loc.Reference.Packing)
		if packer == nil {
			finalErr = eio("couldn't lookup %q key %q file %q", string(h.n.uname), loc.Reference.Key, h.fname)
			continue
		}
		clearLen := packer.UnpackLen(n.f.context, data, &de.Metadata)
		if clearLen < 0 {
			finalErr = eio("couldn't unpack %q key %q file %q", string(h.n.uname), loc.Reference.Key, h.fname)
			continue
		}
		cleartext := make([]byte, clearLen)
		len, err := packer.Unpack(n.f.context, cleartext, data, &de.Metadata, h.n.uname)
		if err != nil {
			finalErr = eio("%s unpacking %q key %q file %q", err, string(h.n.uname), loc.Reference.Key, h.fname)
			continue
		}
		cleartext = cleartext[:len]
		// Save a copy o the cleartext in the local file system.
		if h.file, err = os.Create(h.fname); err != nil {
			return nil, eio("%s creating %q key %q file %q", err, string(h.n.uname), loc.Reference.Key, h.fname)
		}
		if wlen, err := h.file.Write(cleartext); err != nil || len != wlen {
			return nil, eio("%s writing %q key %q file %q", err, string(h.n.uname), loc.Reference.Key, h.fname)
		}
		n.Lock()
		n.attr.Size = uint64(len)
		n.Unlock()
		return h, nil
	}
	return nil, finalErr
}

func fingerprint(loc upspin.Location) string {
	hash := sha1.Sum([]byte(loc.Reference.Key))
	return hex.Dump(hash[:])
}

// Remove implements fs.Noderemover.
// TODO(p): implement Directory.Remove
func (n *node) Remove(context xcontext.Context, req *fuse.RemoveRequest) error {
	fmt.Printf("Remove %q", string(n.uname))
	return nil
}

// Lookup implements fs.NodeStringLookuper.Lookup. 'n' must be a directory.
// We do not use cached knowledge of 'n's contents.
func (n *node) Lookup(context xcontext.Context, name string) (fs.Node, error) {
	fmt.Printf("Lookup %q in %q", name, n.uname)
	if n.t == rootNode {
		// We just assume user directories exist without looking anything up.
		return n.f.allocNode(n, 0700|os.ModeDir, name), nil
	}
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, enotdir(string(n.uname))
	}
	ue, err := n.f.users.lookup(n.user)
	if err != nil {
		return nil, err
	}
	de, err := ue.dir.Lookup(path.Join(n.uname, name))
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0700)
	if de.Metadata.IsDir {
		mode |= os.ModeDir
	}
	nn := n.f.allocNode(n, mode, name)
	return nn, nil
}

// Setattr implements fs.NodeSetattrer.Setattr.
func (n *node) Setattr(context xcontext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	return nil
}

// Flush implements fs.HandleFlusher.Flush.  Called when a file is closed.  This is the point where we
// will write the file to the directory and store.
func (h *handle) Flush(context xcontext.Context, req *fuse.FlushRequest) error {
	return nil
}

// ReadDirAll implements fs.HandleReadDirAller.ReadDirAll.
func (h *handle) ReadDirAll(context xcontext.Context) ([]fuse.Dirent, error) {
	return nil, enotsup("readdirall")
}

// Read implements fs.HandleReader.Read.
func (h *handle) Read(context xcontext.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
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
	n, err := h.file.WriteAt(req.Data, req.Offset)
	if err == nil {
		return err
	}
	resp.Size = n
	newSize := uint64(req.Offset) + uint64(len(req.Data))
	if newSize > h.n.attr.Size {
		h.n.Lock()
		h.n.attr.Size = newSize
		h.n.Unlock()
	}
	return err
}

// Release implements fs.HandleWriter.Release.  This corresponds to a user close and, if the file is dirty,
// should be written back to the store.
// TODO(p): If we fail writing a file, should we try later asynchronously?
func (h *handle) Release(context xcontext.Context, req *fuse.ReleaseRequest) error {
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
	packer := pack.Lookup(h.n.f.context.Packing)
	if packer == nil {
		return eio("unrecognized Packing %d for %q", h.n.f.context.Packing, h.n.uname)
	}
	meta := &upspin.Metadata{}
	// Get a buffer big enough for this data
	cipherLen := packer.PackLen(h.n.f.context, cleartext, meta, upspin.PathName(h.n.uname))
	if cipherLen < 0 {
		return eio("PackLen failed for %q", h.n.uname)
	}
	// TODO: Some packers don't update the meta in PackLen, but some do. If not done, update it now.
	if len(meta.PackData) == 0 {
		meta.PackData = make(upspin.PackData, 1)
		meta.PackData[0] = byte(h.n.f.context.Packing)
	}
	cipher := make([]byte, cipherLen)
	n, err := packer.Pack(h.n.f.context, cipher, cleartext, meta, upspin.PathName(h.n.uname))
	if err != nil {
		return eio("%s Pack(%s)", err, h.n.uname)
	}
	cipher = cipher[:n]
	// Create the directory entry.
	_, err = h.n.f.context.Directory.Put(upspin.PathName(h.n.uname), cipher, meta.PackData)
	if err != nil {
		return eio("%s Directory.Put(%s)", err, h.n.uname)
	}
	return nil
}
