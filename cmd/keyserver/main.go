// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Keyserver is a wrapper for a key implementation that presents it as a grpc interface.
package main

import (
	"flag"
	"net/http"

	gContext "golang.org/x/net/context"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/key/gcp"
	"upspin.io/key/inprocess"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
)

// The upspin username for this server.
const serverName = "keyserver"

// Server is a SecureServer that talks to a KeyServer interface and serves gRPC requests.
type Server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying keyserver implementation.
	key upspin.KeyServer

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	addr := flag.String("addr", "", "publicly accessible network address (host:port)")
	kind := flag.String("kind", "inprocess", "storeserver implementation `kind` (inprocess, gcp)")
	flags.Parse("config", "https", "log", "project")

	if flags.Project != "" {
		log.Connect(flags.Project, serverName)
		svr, err := metric.NewGCPSaver(flags.Project, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	// All we need in the context is some user name. It does not need to be registered as a "real" user.
	context := context.New().SetUserName(serverName)

	// Create a new store implementation.
	var key upspin.KeyServer
	switch *kind {
	case "inprocess":
		key = inprocess.New()
	case "gcp":
		var err error
		key, err = gcp.New(flags.Config...)
		if err != nil {
			log.Fatalf("Setting up KeyServer: %v", err)
		}
	}

	s := &Server{
		context: context,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(*addr),
		},
		key: key,
	}
	authConfig := auth.Config{Lookup: s.internalLookup}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s.SecureServer = grpcSecureServer
	proto.RegisterKeyServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	// TODO(adg): this needs to be changed to keyserver. But it involves some metadata on GCP.
	https.ListenAndServe("userserver", flags.HTTPSAddr, nil)
}

func (s *Server) internalLookup(userName upspin.UserName) (upspin.PublicKey, error) {
	user, err := s.key.Lookup(userName)
	if err != nil {
		return "", err
	}
	return user.PublicKey, nil
}

// keyServerFor returns a KeyServer bound to the user specified in the context.
func (s *Server) keyServerFor(ctx gContext.Context) (upspin.KeyServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := s.key.Dial(s.context.Copy().SetUserName(session.User()), s.endpoint)
	if err != nil {
		return nil, err
	}
	return svc.(upspin.KeyServer), nil
}

// Lookup implements upspin.KeyServer, and does not do any authentication.
func (s *Server) Lookup(ctx gContext.Context, req *proto.KeyLookupRequest) (*proto.KeyLookupResponse, error) {
	log.Printf("Lookup %q", req.UserName)

	user, err := s.key.Lookup(upspin.UserName(req.UserName))
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.UserName, err)
		return &proto.KeyLookupResponse{Error: errors.MarshalError(err)}, nil
	}
	return &proto.KeyLookupResponse{User: proto.UserProto(user)}, nil
}

// keyPutError is
func keyPutError(err error) *proto.KeyPutResponse {
	return &proto.KeyPutResponse{Error: errors.MarshalError(err)}
}

// Put implements upspin.KeyServer.
func (s *Server) Put(ctx gContext.Context, req *proto.KeyPutRequest) (*proto.KeyPutResponse, error) {
	log.Printf("Put %v", req)

	key, err := s.keyServerFor(ctx)
	if err != nil {
		log.Printf("Put %q authentication failed: %v", req.User.Name, err)
		return keyPutError(err), nil

	}
	user := proto.UpspinUser(req.User)
	err = key.Put(user)
	if err != nil {
		log.Printf("Put %q failed: %v", user.Name, err)
		return keyPutError(err), nil
	}
	return &proto.KeyPutResponse{}, nil
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	key, err := s.keyServerFor(ctx)
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

	key, err := s.keyServerFor(ctx)
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
