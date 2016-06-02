// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Store implements upspin.Store on Google Cloud Platform (GCP).
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"

	gContext "golang.org/x/net/context"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/gcp"
	"upspin.io/cmd/store/cache"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	_ "upspin.io/user/gcpuser"
)

var (
	projectID             = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName            = flag.String("bucket", "g-upspin-store", "The name of an existing bucket within the project.")
	tempDir               = flag.String("tempdir", "", "Location of local directory to be our cache. Empty for system default.")
	port                  = flag.Int("port", 5580, "TCP port to serve HTTP requests.")
	noAuth                = flag.Bool("noauth", false, "Disable authentication.")
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

var (
	// Empty structs we can allocate just once.
	endpointResponse  proto.EndpointResponse
	configureResponse proto.ConfigureResponse
)

// grpcStoreServer wraps a storeServer with methods for serving GRPC requests.
type grpcStoreServer struct {
	grpcauth.SecureServer
	store *server
}

// Configure implements the GRPC interface of upspin.Service.
func (s *grpcStoreServer) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Error.Print("Configure not supported")
	return &configureResponse, errors.New("GCP Store server does not support Configure")
}

// Endpoint implements the GRPC interface of upspin.Service.
func (s *grpcStoreServer) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Error.Print("Endpoint not supported")
	return &endpointResponse, errors.New("GCP Store server does not support Endpoint")
}

// ServerUserName implements the GRPC interface of upspin.Service.
func (s *grpcStoreServer) ServerUserName(ctx gContext.Context, req *proto.ServerUserNameRequest) (*proto.ServerUserNameResponse, error) {
	log.Print("ServerUserName")

	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	resp := &proto.ServerUserNameResponse{
		UserName: string(session.User()),
	}
	return resp, nil
}

// Get implements the GRPC interface of upspin.Store.
func (s *grpcStoreServer) Get(ctx gContext.Context, req *proto.StoreGetRequest) (*proto.StoreGetResponse, error) {
	log.Printf("Get %q", req.Reference)

	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	data, locs, err := s.store.Get(session.User(), upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Get %q failed: %v", req.Reference, err)
	}
	resp := &proto.StoreGetResponse{
		Data:      data,
		Locations: proto.Locations(locs),
	}
	return resp, err
}

// Put implements the GRPC interface of upspin.Store.
func (s *grpcStoreServer) Put(ctx gContext.Context, req *proto.StorePutRequest) (*proto.StorePutResponse, error) {
	log.Printf("Put %.30x...", req.Data)

	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	ref, err := s.store.Put(session.User(), req.Data)
	if err != nil {
		log.Printf("Put %.30q failed: %v", req.Data, err)
	}
	resp := &proto.StorePutResponse{
		Reference: string(ref),
	}
	return resp, err
}

// Delete implements the GRPC interface of upspin.Service.
func (s *grpcStoreServer) Delete(ctx gContext.Context, req *proto.StoreDeleteRequest) (*proto.StoreDeleteResponse, error) {
	log.Printf("Delete %q", req.Reference)

	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	err = s.store.Delete(session.User(), upspin.Reference(req.Reference))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Reference, err)
	}
	return nil, err
}

func main() {
	flag.Parse()

	log.Connect("google.com:"+*projectID, *bucketName)
	config := auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	}

	store := &server{
		cloudClient: gcp.New(*projectID, *bucketName, gcp.PublicRead),
		fileCache:   cache.NewFileCache(*tempDir),
	}

	grpcSecureServer, err := grpcauth.NewSecureServer(config, *sslCertificateFile, *sslCertificateKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	s := &grpcStoreServer{
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
