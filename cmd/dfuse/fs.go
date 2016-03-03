// The upspin fuse interface.
package main

import (
	"fmt"
	"os"
	"os/user"
	filepath "path"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	xcontext "golang.org/x/net/context"

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
	uname string // upspin name of node.
	user  upspin.UserName
	attr  fuse.Attr
}

// handle represents an open file.
type handle struct {
	n     *node              // Associated node.
	file  *os.File           // A open file that the in the clear content is stored in.
	fname string             // The name of the open file
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
		n.uname = ""
	} else {
		n.uname = filepath.Join(parent.uname, name)
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
func (n *node) Attr(context xcontext.Context, attr *fuse.Attr) error {
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
	f := n.f
	if n.t == rootNode {
		// User directories are directly below the root.  We can't create
		// them, they are implied.
		return nil, nil, eperm()
	}
	nn := f.allocNode(n, req.Mode&0777, req.Name)
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	h := &handle{n: nn, fname: filepath.Join(n.f.writeDir, fmt.Sprintf(".%d", n.id))}
	var err error
	h.file, err = os.Create(h.fname)
	if err != nil {
		return nil, nil, err
	}
	ue, err := f.users.lookup(nn.user)
	if err != nil {
		return nil, nil, err
	}
	// Make sure we can actually create this node.
	if _, err := ue.dir.Put(upspin.PathName(nn.uname), nil, nil); err != nil {
		// TODO(p): remove from user cache and retry?
		h.file.Close()
		os.Remove(h.fname)
		return nil, nil, err
	}
	resp.Node = h.n.id
	resp.Attr = nn.attr
	resp.EntryValid = time.Hour // TODO(p): figure out what would be right.
	return nn, h, nil
}

// Mkdir implements fs.NodeMkdirer.Mkdir.
// Creates a directory without opening it.
func (n *node) Mkdir(context xcontext.Context, req *fuse.MkdirRequest) (fs.Node, error) {
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
		return nil, err
	}
	return nn, nil
}

// Open implements fs.NodeOpener.Open.  Pertains to files and directories.
// For both, we read the contents on open.
func (n *node) Open(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if req.Dir {
		return n.openDir(context, req, resp)
	}
	return nil, enotsup("open")
}

func (n *node) openDir(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if n.attr.Mode&os.ModeDir != os.ModeDir {
		return nil, enotdir(n.uname)
	}
	return nil, enotsup("openDir")
}

func (n *node) openFile(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if (n.attr.Mode & os.ModeDir) != 0 {
		return nil, eisdir(n.uname)
	}
	return nil, enotsup("openfile")
}

// Remove implements fs.Noderemover.
func (n *node) Remove(context xcontext.Context, req *fuse.RemoveRequest) error {
	return enotsup("remove")
}

// Lookup implements fs.NodeStringLookuper.Lookup assuming that 'n' is a directory.
// Even if we have a cached version of n, we do not use the cache but lookup directly.
func (n *node) Lookup(context xcontext.Context, name string) (fs.Node, error) {
	_, err := n.f.context.Directory.Lookup(upspin.PathName(filepath.Join(string(n.uname), name)))
	if err != nil {
		return nil, err
	}
	return nil, enotsup("lookup")
}

// Setattr implements fs.NodeSetattrer.Setattr.
func (n *node) Setattr(context xcontext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	return enotsup("setattr")
}

// Flush implements fs.HandleFlusher.Flush.  Called when a file is closed.  This is the point where we
// will write the file to the directory and store.
func (h *handle) Flush(context xcontext.Context, req *fuse.FlushRequest) error {
	return enotsup("flush")
}

// ReadAll implements fs.HandleReadAller.ReadAll.
func (h *handle) ReadAll(context xcontext.Context) ([]byte, error) {
	return nil, enotsup("readall")
}

// ReadDirAll implements fs.HandleReadDirAller.ReadDirAll.
func (h *handle) ReadDirAll(context xcontext.Context) ([]fuse.Dirent, error) {
	return nil, enotsup("readdirall")
}

// Read implements fs.HandleReader.Read.
func (h *handle) Read(context xcontext.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	return enotsup("read")
}

// Write implements fs.HandleWriter.Write.
func (h *handle) Write(context xcontext.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	return enotsup("write")
}

// Release implements fs.HandleWriter.Release.
func (h *handle) Release(context xcontext.Context, req *fuse.ReleaseRequest) error {
	return enotsup("release")
}
