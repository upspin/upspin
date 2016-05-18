// Storeserver is a wrapper for a store implementation that presents it as a Go net/rpc interface.
// TODO: Switch to grpc one day.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"

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

	// Load required remote services
	_ "upspin.googlesource.com/upspin.git/directory/remote"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/user/remote"
)

var (
	port    = flag.Int("port", 8080, "TCP port number")
	ctxfile = flag.String("context", os.Getenv("HOME")+"/upspin/rc.storeserver", "context file to use to configure server")
)

type Server struct {
	context *upspin.Context
}

func main() {
	log.SetFlags(log.Lshortfile)
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

	// TODO: FIGURE OUT HTTPS
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcServer := grpc.NewServer()
	proto.RegisterStoreServer(grpcServer, s)
	grpcServer.Serve(listener)
}

func (s *Server) Get(ctx gContext.Context, req *proto.GetRequest) (*proto.GetResponse, error) {
	log.Printf("Get %q", req.Reference)
	data, locs, err := s.context.Store.Get(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Get %q failed: %v", req.Reference, err)
	}
	resp := &proto.GetResponse{
		Data:      data,
		Locations: proto.Locations(locs),
	}
	return resp, err
}

func (s *Server) Put(ctx gContext.Context, req *proto.PutRequest) (*proto.PutResponse, error) {
	log.Printf("Put %.30x...", req.Data)
	ref, err := s.context.Store.Put(req.Data)
	if err != nil {
		log.Printf("Put %.30q failed: %v", req.Data, err)
	}
	resp := &proto.PutResponse{
		Reference: string(ref),
	}
	return resp, err
}

func (s *Server) Delete(ctx gContext.Context, req *proto.DeleteRequest) (*proto.DeleteResponse, error) {
	log.Printf("Delete %q", req.Reference)
	err := s.context.Store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Reference, err)
	}
	return nil, err
}

func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)
	err := s.context.Store.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return nil, err
}

func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")
	endpoint := s.context.Store.Endpoint()
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(endpoint.Transport),
			NetAddr:   string(endpoint.NetAddr),
		},
	}
	return resp, nil
}

func (s *Server) ServerUserName(ctx gContext.Context, req *proto.ServerUserNameRequest) (*proto.ServerUserNameResponse, error) {
	log.Print("ServerUserName")
	userName := s.context.Store.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}
