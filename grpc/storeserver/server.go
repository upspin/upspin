// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storeserver is a wrapper for an upspin.StoreServer implementation
// that presents it as an authenticated GRPC service.
package storeserver

import (
	"fmt"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// server is a SecureServer that talks to a Store interface and serves gRPC requests.
type server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying storage implementation.
	store upspin.StoreServer

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func New(ctx upspin.Context, store upspin.StoreServer, ss grpcauth.SecureServer, addr upspin.NetAddr) proto.StoreServer {
	return &server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		store:        store,
		SecureServer: ss,
	}
}

// storeFor returns a StoreServer instance bound to the user specified in the context.
func (s *server) storeFor(ctx gContext.Context) (upspin.StoreServer, error) {
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

// Get implements proto.StoreServer.
func (s *server) Get(ctx gContext.Context, req *proto.StoreGetRequest) (*proto.StoreGetResponse, error) {
	op := logf("Get %q", req.Reference)

	store, err := s.storeFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.StoreGetResponse{Error: errors.MarshalError(err)}, nil
	}

	data, locs, err := store.Get(upspin.Reference(req.Reference))
	if err != nil {
		op.log(err)
		return &proto.StoreGetResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.StoreGetResponse{
		Data:      data,
		Locations: proto.Locations(locs),
	}
	return resp, nil
}

// Put implements proto.StoreServer.
func (s *server) Put(ctx gContext.Context, req *proto.StorePutRequest) (*proto.StorePutResponse, error) {
	op := logf("Put %.30x...", req.Data)

	store, err := s.storeFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.StorePutResponse{Error: errors.MarshalError(err)}, nil
	}

	ref, err := store.Put(req.Data)
	if err != nil {
		op.log(err)
		return &proto.StorePutResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.StorePutResponse{
		Reference: string(ref),
	}
	return resp, nil
}

// Empty struct we can allocate just once.
var deleteResponse proto.StoreDeleteResponse

// Delete implements proto.StoreServer.
func (s *server) Delete(ctx gContext.Context, req *proto.StoreDeleteRequest) (*proto.StoreDeleteResponse, error) {
	op := logf("Delete %q", req.Reference)

	store, err := s.storeFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.StoreDeleteResponse{Error: errors.MarshalError(err)}, nil
	}

	err = store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		op.log(err)
		return &proto.StoreDeleteResponse{Error: errors.MarshalError(err)}, nil
	}
	return &deleteResponse, nil
}

// Configure implements proto.StoreServer.
func (s *server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	op := logf("Configure %q", req.Options)

	store, err := s.storeFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.ConfigureResponse{Error: errors.MarshalError(err)}, nil
	}

	name, err := store.Configure(req.Options...)
	if err != nil {
		op.log(err)
	}
	return &proto.ConfigureResponse{
		UserName: string(name),
		Error:    errors.MarshalError(err),
	}, nil
}

// Endpoint implements proto.StoreServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	return &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(s.endpoint.Transport),
			NetAddr:   string(s.endpoint.NetAddr),
		},
	}, nil
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Info.Print("grpc/storeserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%v failed: %v", op, err)
}
