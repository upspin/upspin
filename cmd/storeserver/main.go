// Storeserver is a wrapper for a store implementation that presents it as a Go net/rpc interface.
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
	"upspin.googlesource.com/upspin.git/store/proto"
	"upspin.googlesource.com/upspin.git/upspin"

	// TODO: Other than the store implementations, most of these
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
	port    = flag.Int("port", 8080, "TCP port number")
	ctxfile = flag.String("context", os.Getenv("HOME")+"/upspin/rc.storeserver", "context file to use to configure server")
)

type Server struct {
	context *upspin.Context
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("storeserver: ")

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

func (s *Server) Get(req *proto.GetRequest, resp *proto.GetResponse) (err error) {
	log.Printf("Get %q", req.Reference)
	resp.Data, resp.Locations, err = s.context.Store.Get(req.Reference)
	if err != nil {
		log.Printf("Get %q failed: %v", req.Reference, err)
	}
	return err
}

func (s *Server) Put(req *proto.PutRequest, resp *proto.PutResponse) (err error) {
	log.Printf("Put %.30x...", req.Data)
	resp.Reference, err = s.context.Store.Put(req.Data)
	if err != nil {
		log.Printf("Put %.30q failed: %v", req.Data, err)
	}
	return err
}

func (s *Server) Delete(req *proto.DeleteRequest, resp *proto.DeleteResponse) error {
	log.Printf("Delete %q", req.Reference)
	err := s.context.Store.Delete(req.Reference)
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Reference, err)
	}
	return err
}

func (s *Server) Configure(req *proto.ConfigureRequest, resp *proto.ConfigureResponse) error {
	log.Printf("Configure %q", req.Options)
	err := s.context.Store.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return err
}

func (s *Server) Endpoint(req *proto.EndpointRequest, resp *proto.EndpointResponse) error {
	log.Print("Endpoint")
	resp.Endpoint = s.context.Store.Endpoint()
	return nil
}

func (s *Server) ServerUserName(req *proto.ServerUserNameRequest, resp *proto.ServerUserNameResponse) error {
	log.Print("ServerUserName")
	resp.UserName = s.context.Store.ServerUserName()
	return nil
}
