// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storeserver is a wrapper for a store implementation that presents it as a grpc interface.
package main

import (
	"net/http"
	"os"

	gContext "golang.org/x/net/context"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/store/gcp"
	"upspin.io/store/inprocess"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"

	// This is required for context.InitContext to work.
	// TODO(adg): This seems wrong; fix it.
	_ "upspin.io/pack/plain"
)

const serverName = "storeserver"

// Server is a SecureServer that talks to a Store interface and serves gRPC requests.
type Server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying storage implementation.
	store upspin.StoreServer

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	flags.Parse("addr", "config", "context", "https", "kind", "log", "project")

	if flags.Project != "" {
		log.Connect(flags.Project, serverName)
		svr, err := metric.NewGCPSaver(flags.Project, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	// Load context and keys for this server. It needn't have a real username.
	ctxfd, err := os.Open(flags.Context)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	ctx, err := context.InitContext(ctxfd)
	if err != nil && err != context.ErrNoFactotum {
		log.Fatal(err)
	} else if err == nil {
		log.Fatal("storeserver does not use keys, set secrets=none in rc")
	}

	// Create a new store implementation.
	var store upspin.StoreServer
	switch flags.ServerKind {
	case "inprocess":
		store = inprocess.New()
	case "gcp":
		store, err = gcp.New(flags.Config...)
		if err != nil {
			log.Fatalf("Setting up StoreServer: %v", err)
		}
	}

	authConfig := auth.Config{Lookup: auth.PublicUserKeyService(ctx)}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(flags.NetAddr),
		},
		store:        store,
		SecureServer: grpcSecureServer,
	}
	proto.RegisterStoreServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	https.ListenAndServe(serverName, flags.HTTPSAddr, nil)
}

var (
	// Empty structs we can allocate just once.
	deleteResponse    proto.StoreDeleteResponse
	configureResponse proto.ConfigureResponse
)

// storeFor returns a StoreServer instance bound to the user specified in the context.
func (s *Server) storeFor(ctx gContext.Context) (upspin.StoreServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := s.store.Dial(s.context.Copy().SetUserName(session.User()), s.store.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.StoreServer), nil
}

// Get implements upspin.StoreServer.
func (s *Server) Get(ctx gContext.Context, req *proto.StoreGetRequest) (*proto.StoreGetResponse, error) {
	log.Printf("Get %q", req.Reference)

	store, err := s.storeFor(ctx)
	if err != nil {
		return nil, err
	}

	data, locs, err := store.Get(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Get %q failed: %v", req.Reference, err)
		return &proto.StoreGetResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.StoreGetResponse{
		Data:      data,
		Locations: proto.Locations(locs),
	}
	return resp, nil
}

// Put implements upspin.StoreServer.
func (s *Server) Put(ctx gContext.Context, req *proto.StorePutRequest) (*proto.StorePutResponse, error) {
	log.Printf("Put %.30x...", req.Data)

	store, err := s.storeFor(ctx)
	if err != nil {
		return nil, err
	}

	ref, err := store.Put(req.Data)
	if err != nil {
		log.Printf("Put %.30q failed: %v", req.Data, err)
		return &proto.StorePutResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.StorePutResponse{
		Reference: string(ref),
	}
	return resp, nil
}

// Delete implements upspin.StoreServer.
func (s *Server) Delete(ctx gContext.Context, req *proto.StoreDeleteRequest) (*proto.StoreDeleteResponse, error) {
	log.Printf("Delete %q", req.Reference)

	store, err := s.storeFor(ctx)
	if err != nil {
		return nil, err
	}

	err = store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Reference, err)
		return &proto.StoreDeleteResponse{Error: errors.MarshalError(err)}, nil
	}
	return &deleteResponse, nil
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	return nil, errors.Str("Configure not implemented, nor should it be called.")
}

// Endpoint implements upspin.Service
func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(s.endpoint.Transport),
			NetAddr:   string(s.endpoint.NetAddr),
		},
	}
	return resp, nil
}
