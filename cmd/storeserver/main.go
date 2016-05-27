// Storeserver is a wrapper for a store implementation that presents it as a grpc interface.
package main

import (
	"flag"
	"fmt"
	"net"

	gContext "golang.org/x/net/context"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/auth/grpcauth"
	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/upspin/proto"

	// Load required services
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/store/teststore"

	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
	_ "upspin.googlesource.com/upspin.git/user/remote"
	_ "upspin.googlesource.com/upspin.git/user/testuser"
)

var (
	port         = flag.Int("port", 8080, "TCP port number")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	noAuth       = flag.Bool("noauth", false, "Disable authentication.")
	certFile     = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	certKeyFile  = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

// Server is a SecureServer that talks to a Store interface and serves gRPC requests.
type Server struct {
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
	store upspin.Store
}

func main() {
	flag.Parse()
	log.Connect("google.com:upspin", "storeserver")

	if *noAuth {
		*certFile = ""
		*certKeyFile = ""
	}

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
	config := auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	}
	grpcSecureServer, err := grpcauth.NewSecureServer(config, *certFile, *certKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		SecureServer: grpcSecureServer,
		store:        store,
	}

	proto.RegisterStoreServer(grpcSecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcSecureServer.Serve(listener)
}

// Get implements upspin.Store
func (s *Server) Get(ctx gContext.Context, req *proto.StoreGetRequest) (*proto.StoreGetResponse, error) {
	log.Printf("Get %q", req.Reference)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	data, locs, err := s.store.Get(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Get %q failed: %v", req.Reference, err)
	}
	resp := &proto.StoreGetResponse{
		Data:      data,
		Locations: proto.Locations(locs),
	}
	return resp, err
}

// Put implements upspin.Store
func (s *Server) Put(ctx gContext.Context, req *proto.StorePutRequest) (*proto.StorePutResponse, error) {
	log.Printf("Put %.30x...", req.Data)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	ref, err := s.store.Put(req.Data)
	if err != nil {
		log.Printf("Put %.30q failed: %v", req.Data, err)
	}
	resp := &proto.StorePutResponse{
		Reference: string(ref),
	}
	return resp, err
}

// Delete implements upspin.Store
func (s *Server) Delete(ctx gContext.Context, req *proto.StoreDeleteRequest) (*proto.StoreDeleteResponse, error) {
	log.Printf("Delete %q", req.Reference)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	err = s.store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Reference, err)
	}
	return nil, err
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)
	err := s.store.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return nil, err
}

// Endpoint implements upspin.Service
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

// ServerUserName implements upspin.Service
func (s *Server) ServerUserName(ctx gContext.Context, req *proto.ServerUserNameRequest) (*proto.ServerUserNameResponse, error) {
	log.Print("ServerUserName")
	userName := s.store.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}
