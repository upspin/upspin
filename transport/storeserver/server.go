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
	"upspin.io/log"
	"upspin.io/transport/auth"
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

	return auth.NewServer(ctx, &auth.ServerConfig{
		Service: auth.Service{
			Name: "Store",
			Methods: auth.Methods{
				"Get":    s.Get,
				"Put":    s.Put,
				"Delete": s.Delete,
			},
		},
	})
}

func (s *server) serverFor(session auth.Session, reqBytes []byte, req pb.Message) (upspin.StoreServer, error) {
	if err := pb.Unmarshal(reqBytes, req); err != nil {
		return nil, err
	}
	svc, err := s.store.Dial(context.SetUserName(s.context, session.User()), s.store.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.StoreServer), nil
}

// Get implements proto.StoreServer.
func (s *server) Get(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.StoreGetRequest
	store, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
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
func (s *server) Put(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.StorePutRequest
	store, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
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
func (s *server) Delete(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.StoreGetRequest
	store, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf("Delete %q", req.Reference)

	err = store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		op.log(err)
		return &proto.StoreDeleteResponse{Error: errors.MarshalError(err)}, nil
	}
	return &deleteResponse, nil
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Info.Print("transport/storeserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%v failed: %v", op, err)
}
