// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storeserver is a wrapper for an upspin.StoreServer implementation
// that presents it as an authenticated GRPC service.
package storeserver

import (
	"fmt"
	"net/http"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/grpc/auth"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// server is a SecureServer that talks to a Store interface and serves GRPC requests.
type server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying storage implementation.
	store upspin.StoreServer

	// For session handling and the Ping GRPC method.
	auth.Server
}

func New(ctx upspin.Context, store upspin.StoreServer, addr upspin.NetAddr) http.Handler {
	s := &server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		store: store,
	}

	return auth.NewServer(ctx, &auth.ServerConfig{Service: s})
}

func (s *server) Service() string {
	return "StoreServer"
}

func (s *server) RequestMessage(method string) pb.Message {
	switch method {
	case "Get":
		return new(proto.StoreGetRequest)
	case "Put":
		return new(proto.StorePutRequest)
	case "Delete":
		return new(proto.StoreDeleteRequest)
	}
	return nil
}

func (s *server) Dispatch(userName upspin.UserName, method string, in pb.Message) (pb.Message, error) {
	svc, err := s.store.Dial(context.SetUserName(s.context, userName), s.store.Endpoint())
	if err != nil {
		return nil, err
	}
	store := svc.(upspin.StoreServer)
	switch method {
	case "Get":
		return s.Get(store, in.(*proto.StoreGetRequest))
	case "Put":
		return s.Put(store, in.(*proto.StorePutRequest))
	case "Delete":
		return s.Delete(store, in.(*proto.StoreDeleteRequest))
	}
	return nil, errors.Str("invalid method")
}

// Get implements proto.StoreServer.
func (s *server) Get(store upspin.StoreServer, req *proto.StoreGetRequest) (*proto.StoreGetResponse, error) {
	op := logf("Get %q", req.Reference)

	data, refdata, locs, err := store.Get(upspin.Reference(req.Reference))
	if err != nil {
		op.log(err)
		return &proto.StoreGetResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.StoreGetResponse{
		Data:      data,
		Refdata:   proto.RefdataProto(refdata),
		Locations: proto.Locations(locs),
	}
	return resp, nil
}

// Put implements proto.StoreServer.
func (s *server) Put(store upspin.StoreServer, req *proto.StorePutRequest) (*proto.StorePutResponse, error) {
	op := logf("Put %.30x...", req.Data)

	refdata, err := store.Put(req.Data)
	if err != nil {
		op.log(err)
		return &proto.StorePutResponse{Error: errors.MarshalError(err)}, nil
	}
	resp := &proto.StorePutResponse{
		Refdata: proto.RefdataProto(refdata),
	}
	return resp, nil
}

// Empty struct we can allocate just once.
var deleteResponse proto.StoreDeleteResponse

// Delete implements proto.StoreServer.
func (s *server) Delete(store upspin.StoreServer, req *proto.StoreDeleteRequest) (*proto.StoreDeleteResponse, error) {
	op := logf("Delete %q", req.Reference)

	err := store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		op.log(err)
		return &proto.StoreDeleteResponse{Error: errors.MarshalError(err)}, nil
	}
	return &deleteResponse, nil
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
