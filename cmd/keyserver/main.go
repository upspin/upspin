// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as a grpc interface.
package main

import (
	"flag"
	"net/http"
	"strings"

	gContext "golang.org/x/net/context"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
)

var project = flag.String("project", "", "The GCP project name, if any.")

// The upspin username for this server.
const serverName = "keyserver"

// Server is a SecureServer that talks to a KeyServer interface and serves gRPC requests.
type Server struct {
	context  upspin.Context
	endpoint upspin.Endpoint
	key      upspin.KeyServer // default user service for looking up keys for unauthenticated users.
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	flag.Parse()

	if *project != "" {
		log.Connect(*project, serverName)
		svr, err := metric.NewGCPSaver(*project, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", *project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	endpoint, err := upspin.ParseEndpoint(flags.Endpoint)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// All we need in the context is some user name. It does not need to be registered as a "real" user.
	context := context.New().SetUserName(serverName)

	// Get an instance so we can configure it and use it for authenticated connections.
	key, err := bind.KeyServer(context, *endpoint)
	if err != nil {
		log.Fatal(err)
	}

	// If there are configuration options, set them now
	if flags.Config != "" {
		opts := strings.Split(flags.Config, ",")
		// Configure it appropriately.
		log.Printf("Configuring server with options: %v", opts)
		err = key.Configure(opts...)
		if err != nil {
			log.Fatal(err)
		}
		// Now this pre-configured store is the one that will generate new instances for this server.
		err = bind.ReregisterKeyServer(endpoint.Transport, key)
		if err != nil {
			log.Fatal(err)
		}
	}

	s := &Server{
		context:  context,
		endpoint: *endpoint,
		key:      key,
	}
	authConfig := auth.Config{Lookup: s.internalLookup}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s.SecureServer = grpcSecureServer
	proto.RegisterKeyServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	// TODO: this needs to be changed to keyserver. But it involves some metadata on GCP.
	https.ListenAndServe("userserver", flags.HTTPSAddr, nil)
}

func (s *Server) internalLookup(userName upspin.UserName) (upspin.PublicKey, error) {
	user, err := s.key.Lookup(userName)
	return user.PublicKey, err
}

// keyServerFor returns a KeyServer bound to the user specified in the context and the username authenticated.
func (s *Server) keyServerFor(ctx gContext.Context) (upspin.KeyServer, upspin.UserName, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	context := s.context.Copy().SetUserName(session.User())
	key, err := bind.KeyServer(context, s.endpoint)
	return key, session.User(), err
}

// Lookup implements upspin.KeyServer, and does not do any authentication.
func (s *Server) Lookup(ctx gContext.Context, req *proto.KeyLookupRequest) (*proto.KeyLookupResponse, error) {
	log.Printf("Lookup %q", req.UserName)

	user, err := s.key.Lookup(upspin.UserName(req.UserName))
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.UserName, err)
		return &proto.KeyLookupResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.KeyLookupResponse{
		UserName:  string(user.Name),
		Dirs:      proto.Endpoints(user.Dirs),
		Stores:    proto.Endpoints(user.Stores),
		PublicKey: string(user.PublicKey),
	}
	return resp, nil
}

// Put implements upspin.KeyServer.
func (s *Server) Put(ctx gContext.Context, req *proto.KeyPutRequest) (*proto.KeyPutResponse, error) {
	log.Printf("Put %v", req)

	key, userName, err := s.keyServerFor(ctx)
	if err != nil {
		log.Printf("Put %q authentication failed: %v", req.UserName, err)
		return &proto.KeyPutResponse{Error: errors.MarshalError(err)}, nil

	}
	user := &upspin.User{
		Name:      upspin.UserName(req.UserName),
		Dirs:      proto.UpspinEndpoints(req.Dirs),
		Stores:    proto.UpspinEndpoints(req.Stores),
		PublicKey: upspin.PublicKey(req.PublicKey),
	}
	// If the user is editing someone else's entry, it must be an admin.
	if userName != user.Name {
		// TODO: check if userName is an admin.
		err := errors.E("Put", errors.Permission, errors.Errorf("%q not authorized to edit %q data", userName, req.UserName))
		log.Error.Printf("Put: %v", err)
		return &proto.KeyPutResponse{Error: errors.MarshalError(err)}, nil
	}

	err = key.Put(user)
	if err != nil {
		log.Printf("Put %q failed: %v", req.UserName, err)
		return &proto.KeyPutResponse{Error: errors.MarshalError(err)}, nil
	}
	return &proto.KeyPutResponse{}, nil
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	key, _, err := s.keyServerFor(ctx)
	if err != nil {
		return nil, err
	}
	err = key.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
		return &proto.ConfigureResponse{Error: errors.MarshalError(err)}, nil
	}
	return nil, nil
}

// Endpoint implements upspin.Service
func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")

	key, _, err := s.keyServerFor(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := key.Endpoint()
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(endpoint.Transport),
			NetAddr:   string(endpoint.NetAddr),
		},
	}
	return resp, nil
}
