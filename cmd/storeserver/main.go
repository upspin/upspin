// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Storeserver is a wrapper for a store implementation that presents it as a grpc interface.
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
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"
)

var (
	httpsAddr    = flag.String("https_addr", "localhost:8000", "HTTPS listen address")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	config       = flag.String("config", "", "Comma-separated list of configuration options for this server")
	logFile      = flag.String("logfile", "storeserver", "Name of the log file on GCP or empty for no GCP logging")
)

// Server is a SecureServer that talks to a Store interface and serves gRPC requests.
type Server struct {
	context  *upspin.Context
	endpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	flag.Parse()

	if *logFile != "" {
		log.Connect("google.com:upspin", *logFile)
	}

	endpoint, err := upspin.ParseEndpoint(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// All we need in the context is some user name. It does not need to be registered as a "real" user.
	context := &upspin.Context{
		UserName: "storeserver",
	}
	// If there are configuration options, set them now
	if *config != "" {
		// Get an instance so we can configure it.
		store, err := bind.Store(context, *endpoint)
		if err != nil {
			log.Fatal(err)
		}
		opts := strings.Split(*config, ",")
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

	authConfig := auth.Config{Lookup: auth.PublicUserKeyService()}
	grpcSecureServer, err := grpcauth.NewSecureServer(authConfig)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context:      context,
		endpoint:     *endpoint,
		SecureServer: grpcSecureServer,
	}
	proto.RegisterStoreServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	https.ListenAndServe("storeserver", *httpsAddr, nil)
}

var (
	// Empty structs we can allocate just once.
	deleteResponse    proto.StoreDeleteResponse
	configureResponse proto.ConfigureResponse
)

// storeFor returns a Store service bound to the user specified in the context.
func (s *Server) storeFor(ctx gContext.Context) (upspin.Store, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	context := *s.context
	context.UserName = session.User()
	return bind.Store(&context, s.endpoint)
}

// Get implements upspin.Store
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

// Put implements upspin.Store
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

// Delete implements upspin.Store
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
	log.Printf("Configure %q", req.Options)

	store, err := s.storeFor(ctx)
	if err != nil {
		return nil, err
	}

	err = store.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
		return &proto.ConfigureResponse{Error: errors.MarshalError(err)}, nil
	}
	return &configureResponse, nil
}

// Endpoint implements upspin.Service
func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")

	store, err := s.storeFor(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := store.Endpoint()
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(endpoint.Transport),
			NetAddr:   string(endpoint.NetAddr),
		},
	}
	return resp, nil
}
