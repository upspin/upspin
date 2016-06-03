// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Userserver is a wrapper for a user implementation that presents it as a Go net/rpc interface.
package main

import (
	"flag"
	"fmt"
	"net"
	"strings"

	gContext "golang.org/x/net/context"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/endpoint"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/user/transports"
)

var (
	port         = flag.Int("port", 8082, "TCP port number")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	noAuth       = flag.Bool("noauth", false, "Disable authentication.")
	certFile     = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	certKeyFile  = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
	config       = flag.String("config", "", "Comma-separated list of configuration options (key=value) for this server")
)

// Server is a SecureServer that talks to a User interface and serves gRPC requests.
type Server struct {
	context  *upspin.Context
	endpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	flag.Parse()
	log.Connect("google.com:upspin", "userserver")

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
		UserName: "userserver",
	}

	// If there are configuration options, set them now
	if *config != "" {
		// Get an instance so we can configure it.
		store, err := bind.Store(context, *endpoint)
		if err != nil {
			log.Fatal(err)
		}
		opts := strings.Split(*config, ",")
		if len(opts)%2 != 0 {
			log.Fatal("Configuration options must have format optKey,optVal,...")
		}
		// Configure it appropriately.
		log.Printf("Configuring server with options: %v", opts)
		err = store.Configure(opts...)
		if err != nil {
			log.Fatal(err)
		}
		// Now this pre-configured store is the one that will generate new instances for this server.
		err = bind.ReregisterStore(endpoint.Transport, store)
		if err != nil {
			log.Fatal(err)
		}
	}

	authConfig := auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig, *certFile, *certKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context:      context,
		endpoint:     *endpoint,
		SecureServer: grpcSecureServer,
	}

	proto.RegisterUserServer(grpcSecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcSecureServer.Serve(listener)
}

// userFor returns a User service bound to the user specified in the context.
func (s *Server) userFor(ctx gContext.Context) (upspin.User, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	context := *s.context
	context.UserName = session.User()
	return bind.User(&context, s.endpoint)
}

// Lookup implements upspin.User
func (s *Server) Lookup(ctx gContext.Context, req *proto.UserLookupRequest) (*proto.UserLookupResponse, error) {
	log.Printf("Lookup %q", req.UserName)

	user, err := s.userFor(ctx)
	if err != nil {
		return nil, err
	}

	endpoints, publicKeys, err := user.Lookup(upspin.UserName(req.UserName))
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.UserName, err)
	}
	resp := &proto.UserLookupResponse{
		Endpoints:  proto.Endpoints(endpoints),
		PublicKeys: proto.PublicKeys(publicKeys),
	}
	return resp, err
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	user, err := s.userFor(ctx)
	if err != nil {
		return nil, err
	}
	err = user.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return nil, err
}

// Endpoint implements upspin.Service
func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")

	user, err := s.userFor(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := user.Endpoint()
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
	user, err := s.userFor(ctx)
	if err != nil {
		return nil, err
	}
	userName := user.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}
