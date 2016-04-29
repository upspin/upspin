// Dirserver is a wrapper for a directory implementation that presents it as a Go net/rpc interface.
// TODO: Switch to grpc one day.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"

	"upspin.googlesource.com/upspin.git/context"
	"upspin.googlesource.com/upspin.git/directory/proto"
	"upspin.googlesource.com/upspin.git/upspin"

	// TODO: Other than the directory implementations, most of these
	// are only necessary because of InitContext.

	// Load useful packers
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"

	// Load required gcp services
	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"

	// Load required test services
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/testuser"
)

var (
	port    = flag.Int("port", 8081, "TCP port number")
	ctxfile = flag.String("context", os.Getenv("HOME")+"/upspin/rc.dirserver", "context file to use to configure server")
)

type Server struct {
	context *upspin.Context
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("dirserver: ")

	flag.Parse()

	ctxfd, err := os.Open(*ctxfile)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	ctx, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context: ctx,
	}

	rpc.Register(s)
	// TODO: FIGURE OUT HTTPS
	rpc.HandleHTTP()
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	log.Fatal(http.Serve(listener, nil))
}

func (s *Server) Lookup(req *proto.LookupRequest, resp *proto.LookupResponse) (err error) {
	log.Printf("Lookup %q", req.Name)
	resp.Entry, err = s.context.Directory.Lookup(req.Name)
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) Put(req *proto.PutRequest, resp *proto.PutResponse) error {
	log.Printf("Put %q", req.Entry.Name)
	err := s.context.Directory.Put(req.Entry)
	if err != nil {
		log.Printf("Put %q failed: %v", req.Entry.Name, err)
	}
	return err
}

func (s *Server) MakeDirectory(req *proto.MakeDirectoryRequest, resp *proto.MakeDirectoryResponse) (err error) {
	log.Printf("MakeDirectory %q", req.Name)
	resp.Location, err = s.context.Directory.MakeDirectory(req.Name)
	if err != nil {
		log.Printf("MakeDirectory %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) Glob(req *proto.GlobRequest, resp *proto.GlobResponse) (err error) {
	log.Printf("Glob %q", req.Pattern)
	resp.Entries, err = s.context.Directory.Glob(req.Pattern)
	if err != nil {
		log.Printf("Glob %q failed: %v", req.Pattern, err)
	}
	return err
}

func (s *Server) Delete(req *proto.DeleteRequest, resp *proto.DeleteResponse) error {
	log.Printf("Delete %q", req.Name)
	err := s.context.Directory.Delete(req.Name)
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) WhichAccess(req *proto.WhichAccessRequest, resp *proto.WhichAccessResponse) (err error) {
	log.Printf("WhichAccess %q", req.Name)
	resp.Name, err = s.context.Directory.WhichAccess(req.Name)
	if err != nil {
		log.Printf("WhichAccess %q failed: %v", req.Name, err)
	}
	return err
}
