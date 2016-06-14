// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Userserver is a wrapper for a user implementation that presents it as a Go net/rpc interface.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path"
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
	port         = flag.Int("port", 5582, "TCP port number")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	selfSigned   = flag.Bool("selfsigned", false, "Start server with a self-signed TLS certificate")
	certFile     = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to TLS certificate file")
	certKeyFile  = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to TLS certificate key file")
	config       = flag.String("config", "", "Comma-separated list of configuration options (key=value) for this server")
	logFile      = flag.String("logfile", "userserver", "Name of the log file on GCP or empty for no GCP logging")
)

// The upspin username for this server.
const serverName = "userserver"

// Server is a SecureServer that talks to a User interface and serves gRPC requests.
type Server struct {
	context  *upspin.Context
	endpoint upspin.Endpoint
	user     upspin.User // default user service for looking up keys for unauthenticated users.
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	flag.Parse()

	if *logFile != "" {
		log.Connect("google.com:upspin", *logFile)
	}

	if *selfSigned {
		*certFile = path.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/cert.pem")
		*certKeyFile = path.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/key.pem")
	}

	endpoint, err := endpoint.Parse(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// All we need in the context is some user name. It does not need to be registered as a "real" user.
	context := &upspin.Context{
		UserName: serverName,
	}

	// Get an instance so we can configure it and use it for authenticated connections.
	user, err := bind.User(context, *endpoint)
	if err != nil {
		log.Fatal(err)
	}

	// If there are configuration options, set them now
	if *config != "" {
		opts := strings.Split(*config, ",")
		// Configure it appropriately.
		log.Printf("Configuring server with options: %v", opts)
		err = user.Configure(opts...)
		if err != nil {
			log.Fatal(err)
		}
		// Now this pre-configured store is the one that will generate new instances for this server.
		err = bind.ReregisterUser(endpoint.Transport, user)
		if err != nil {
			log.Fatal(err)
		}
	}

	s := &Server{
		context:  context,
		endpoint: *endpoint,
		user:     user,
	}
	authConfig := auth.Config{
		Lookup: s.internalLookup,
		AllowUnauthenticatedConnections: *selfSigned,
	}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig, *certFile, *certKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	s.SecureServer = grpcSecureServer
	proto.RegisterUserServer(grpcSecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcSecureServer.Serve(listener)
}

func (s *Server) internalLookup(userName upspin.UserName) ([]upspin.PublicKey, error) {
	_, keys, err := s.user.Lookup(userName)
	return keys, err
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

// Lookup implements upspin.User, and does not do any authentication.
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
