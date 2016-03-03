// Hellofs implements a simple "hello world" file system.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	filepath "path"
	"sync"
	"time"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"
	xcontext "golang.org/x/net/context"

	"upspin.googlesource.com/upspin.git/context"
	//"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s MOUNTPOINT\n", os.Args[0])
	flag.PrintDefaults()
}

// ufs represents an instance of the file system.
type ufs struct {
	sync.Mutex
	root     *node
	cacheDir string // Directory for in the clear cached files.
	writeDir string // Directory for files being written.
	context  *upspin.Context
	uid      int
	gid      int
	lastId   fuse.NodeID
}

type node struct {
	sync.Mutex
	id    fuse.NodeID
	fs    *ufs
	uname string
	attr  fuse.Attr
}

// handle represents an open file.
type handle struct {
	n     *node    // Associated node.
	f     *os.File // A open file that the in the clear content is stored in.
	fname string   // The name of that file
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	mountpoint := flag.Arg(0)

	f := newUfs()

	c, err := fuse.Mount(
		mountpoint,
		fuse.FSName("upspin"),
		fuse.Subtype("fs"),
		fuse.LocalVolume(),
		fuse.VolumeName(string(f.context.UserName)),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	err = fs.Serve(c, f)
	if err != nil {
		log.Fatal(err)
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		log.Fatal(err)
	}
}

// newUfs creates a new user file system filling in the context from the environment.
func newUfs() *ufs {
	f := &ufs{
		uid: os.Getuid(),
		gid: os.Getgid(),
	}
	var err error
	if f.context, err = context.InitContext(nil); err != nil {
		log.Fatal(err)
	}
	f.root = f.allocNode(nil, 0700|os.ModeDir, "")
	homeDir := "/tmp"
	if u, err := user.Current(); err == nil {
		if len(u.HomeDir) != 0 {
			homeDir = u.HomeDir
		}
	}
	f.writeDir = homeDir + "/.upspin/wb"
	f.cacheDir = homeDir + "/.upspin/cache"
	return f
}

func (f *ufs) Root() (fs.Node, error) {
	return f.root, nil
}

func (f *ufs) allocNodeId() fuse.NodeID {
	f.Lock()
	defer f.Unlock()
	f.lastId++
	return f.lastId
}

func (f *ufs) allocNode(parent *node, mode os.FileMode, name string) *node {
	n := &node{id: f.allocNodeId()}
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
	if parent != nil {
		n.uname = filepath.Join(parent.uname, name)
	} else {
		n.uname = name
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
// written to upspin on close.
func (n *node) Create(context xcontext.Context, req *fuse.CreateRequest, resp *fuse.CreateResponse) (fs.Node, fs.Handle, error) {
	f := n.fs
	nn := f.allocNode(n, req.Mode&0777, req.Name)
	nn.attr.Uid = req.Header.Uid
	nn.attr.Gid = req.Header.Gid
	// Make sure we can actually create this node.
	h := &handle{n: nn, fname: filepath.Join(n.fs.writeDir, fmt.Sprintf(".%d", n.id))}
	var err error
	h.f, err = os.Create(h.fname)
	if err != nil {
		return nil, nil, err
	}
	if _, err := f.context.Directory.Put(upspin.PathName(nn.uname), nil, nil); err != nil {
		h.f.Close()
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
	nn := n.fs.allocNode(n, (req.Mode&0777)|os.ModeDir, req.Name)
	nn.attr = fuse.Attr{
		Mode:   req.Mode | os.ModeDir,
		Atime:  now,
		Ctime:  now,
		Mtime:  now,
		Crtime: now,
		Uid:    req.Header.Uid,
		Gid:    req.Header.Gid,
	}
	if _, err := n.fs.context.Directory.MakeDirectory(upspin.PathName(nn.uname)); err != nil {
		return nil, err
	}
	return nn, nil
}

// Open implements fs.NodeOpener.Open.  Pertains to files and directories.
func (n *node) Open(context xcontext.Context, req *fuse.OpenRequest, resp *fuse.OpenResponse) (fs.Handle, error) {
	if req.Dir {
		if n.attr.Mode&os.ModeDir != os.ModeDir {
			return nil, errors.New("file does not exist")
		}
	}
	return nil, errors.New("unimplemented")
}

// Remove implements fs.Noderemover.
func (n *node) Remove(context xcontext.Context, req *fuse.RemoveRequest) error {
	return errors.New("unimplemented")
}

// Lookup implements fs.NodeStringLookuper.Lookup.
func (n *node) Lookup(context xcontext.Context, name string) (fs.Node, error) {
	return nil, errors.New("unimplemented")
}

// Setattr implements fs.NodeSetattrer.Setattr.
func (n *node) Setattr(context xcontext.Context, req *fuse.SetattrRequest, resp *fuse.SetattrResponse) error {
	return errors.New("unimplemented")
}

// Flush implements fs.HandleFlusher.Flush.  Called when a file is closed.  This is the point where we
// will write the file to the directory and store.
func (h *handle) Flush(context xcontext.Context, req *fuse.FlushRequest) error {
	return errors.New("unimplemented")
}

// ReadAll implements fs.HandleReadAller.ReadAll.
func (h *handle) ReadAll(context xcontext.Context) ([]byte, error) {
	return nil, errors.New("unimplemented")
}

// ReadDirAll implements fs.HandleReadDirAller.ReadDirAll.
func (h *handle) ReadDirAll(context xcontext.Context) ([]fuse.Dirent, error) {
	return nil, errors.New("unimplemented")
}

// Read implements fs.HandleReader.Read.
func (h *handle) Read(context xcontext.Context, req *fuse.ReadRequest, resp *fuse.ReadResponse) error {
	return errors.New("unimplemented")
}

// Write implements fs.HandleWriter.Write.
func (h *handle) Write(context xcontext.Context, req *fuse.WriteRequest, resp *fuse.WriteResponse) error {
	return errors.New("unimplemented")
}

// Release implements fs.HandleWriter.Release.
func (h *handle) Release(context xcontext.Context, req *fuse.ReleaseRequest) error {
	return errors.New("unimplemented")
}
