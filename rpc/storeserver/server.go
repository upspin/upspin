// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storeserver is a wrapper for an upspin.StoreServer implementation
// that presents it as an authenticated service.
package storeserver // import "upspin.io/rpc/storeserver"

import (
	"fmt"
	"net/http"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/rpc"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

type server struct {
	config upspin.Config

	// The underlying storage implementation.
	store upspin.StoreServer
}

func New(cfg upspin.Config, store upspin.StoreServer, _ upspin.NetAddr) http.Handler {
	// TODO(adg): remove addr argument
	s := &server{
		config: cfg,
		store:  store,
	}

	return rpc.NewServer(cfg, rpc.Service{
		Name: "Store",
		Methods: map[string]rpc.Method{
			"Get":    s.Get,
			"Put":    s.Put,
			"Delete": s.Delete,
		},
	})
}

func (s *server) serverFor(session rpc.Session, reqBytes []byte, req pb.Message) (upspin.StoreServer, error) {
	if err := pb.Unmarshal(reqBytes, req); err != nil {
		return nil, err
	}
	e := s.store.Endpoint()
	if ep := session.ProxiedEndpoint(); ep.Transport != upspin.Unassigned {
		e = ep
	}
	svc, err := s.store.Dial(config.SetUserName(s.config, session.User()), e)
	if err != nil {
		return nil, err
	}
	return svc.(upspin.StoreServer), nil
}

// Get implements proto.StoreServer.
func (s *server) Get(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.StoreGetRequest
	store, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := s.logf(session, "Get(%q)", req.Reference)

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
func (s *server) Put(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.StorePutRequest
	store, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := s.logf(session, "Put(%.16x...) (%d bytes)", req.Data, len(req.Data))

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
func (s *server) Delete(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.StoreGetRequest
	store, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := s.logf(session, "Delete(%q)", req.Reference)

	err = store.Delete(upspin.Reference(req.Reference))
	if err != nil {
		op.log(err)
		return &proto.StoreDeleteResponse{Error: errors.MarshalError(err)}, nil
	}
	return &deleteResponse, nil
}

func (s *server) logf(sess rpc.Session, format string, args ...interface{}) operation {
	op := fmt.Sprintf("rpc/storeserver: %q: store.", sess.User())
	op += fmt.Sprintf(format, args...)
	log.Debug.Print(op)
	return operation(op)
}

type operation string

func (op operation) log(err error) {
	log.Debug.Print(op)
}
