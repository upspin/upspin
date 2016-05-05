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
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/proto"

	// TODO: Other than the user implementations, most of these
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

	// Load required remote services
	_ "upspin.googlesource.com/upspin.git/directory/remote"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/user/remote"
)

var (
	port    = flag.Int("port", 8082, "TCP port number")
	ctxfile = flag.String("context", os.Getenv("HOME")+"/upspin/rc.userserver", "context file to use to configure server")
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
	log.Printf("Lookup %q", req.UserName)
	resp.Endpoints, resp.PublicKeys, err = s.context.User.Lookup(req.UserName)
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.UserName, err)
	}
	return err
}

func (s *Server) Configure(req *proto.ConfigureRequest, resp *proto.ConfigureResponse) error {
	log.Printf("Configure %q", req.Options)
	err := s.context.User.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return err
}

func (s *Server) Endpoint(req *proto.EndpointRequest, resp *proto.EndpointResponse) error {
	log.Print("Endpoint")
	resp.Endpoint = s.context.User.Endpoint()
	return nil
}

func (s *Server) ServerUserName(req *proto.ServerUserNameRequest, resp *proto.ServerUserNameResponse) error {
	log.Print("ServerUserName")
	resp.UserName = s.context.User.ServerUserName()
	return nil
}
