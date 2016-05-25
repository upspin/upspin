// Userserver is a wrapper for a user implementation that presents it as a Go net/rpc interface.
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
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
	_ "upspin.googlesource.com/upspin.git/user/remote"
	_ "upspin.googlesource.com/upspin.git/user/testuser"
)

var (
	port         = flag.Int("port", 8082, "TCP port number")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	noAuth       = flag.Bool("noauth", false, "Disable authentication.")
	certFile     = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	certKeyFile  = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

// Server is a SecureServer that talks to a Store interface and serves gRPC requests.
type Server struct {
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
	user upspin.User
}

func main() {
	flag.Parse()
	log.Connect("google.com:upspin", "userserver")

	endpoint, err := endpoint.Parse(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// All we need in the context is some user name. It is unauthenticated. TODO?
	context := &upspin.Context{
		UserName: "userserver",
	}

	user, err := bind.User(context, *endpoint)
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
		user:         user,
	}

	proto.RegisterUserServer(grpcSecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcSecureServer.Serve(listener)
}

func (s *Server) Lookup(ctx gContext.Context, req *proto.UserLookupRequest) (*proto.UserLookupResponse, error) {
	log.Printf("Lookup %q", req.UserName)
	endpoints, publicKeys, err := s.user.Lookup(upspin.UserName(req.UserName))
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.UserName, err)
	}
	resp := &proto.UserLookupResponse{
		Endpoints:  proto.Endpoints(endpoints),
		PublicKeys: proto.PublicKeys(publicKeys),
	}
	return resp, err
}

func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)
	err := s.user.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return nil, err
}

func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")
	endpoint := s.user.Endpoint()
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
	userName := s.user.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}

func (s *Server) Ping(ctx gContext.Context, req *proto.PingRequest) (*proto.PingResponse, error) {
	log.Print("Ping")
	resp := &proto.PingResponse{
		PingSequence: req.PingSequence,
	}
	return resp, nil
}
