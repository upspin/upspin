// Storeserver is a wrapper for a store implementation that presents it as a Go net/rpc interface.
// TODO: Switch to grpc one day.
package main

import (
	"flag"
	"fmt"
	"net"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/upspin/proto"

	// Load required services
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

var (
	port         = flag.Int("port", 8080, "TCP port number")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
)

type Server struct {
	store upspin.Store
}

func main() {
	flag.Parse()
	log.Connect("google.com:upspin", "storeserver")

	endpoint, err := endpoint.Parse(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// All we need in the context is some user name. It is unauthenticated. TODO?
	context := &upspin.Context{
		UserName: "storeserver",
	}

	store, err := bind.Store(context, *endpoint)
	if err != nil {
		log.Fatalf("binding to %q: %v", *endpoint, err)
	}

	s := &Server{
		store: store,
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
	data, locs, err := s.store.Get(upspin.Reference(req.Reference))
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
	ref, err := s.store.Put(req.Data)
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
	err := s.store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Reference, err)
	}
	return nil, err
}

func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)
	err := s.store.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return nil, err
}

func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")
	endpoint := s.store.Endpoint()
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
	userName := s.store.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}
