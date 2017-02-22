// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package keyserver is a wrapper for an upspin.KeyServer implementation
// that presents it as an authenticated service.
package keyserver // import "upspin.io/rpc/keyserver"

import (
	"fmt"
	"math/rand"
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

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying keyserver implementation.
	key upspin.KeyServer
}

// New creates a new instance of the RPC key server.
func New(cfg upspin.Config, key upspin.KeyServer, addr upspin.NetAddr) http.Handler {
	s := &server{
		config: cfg,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		key: key,
	}
	return rpc.NewServer(cfg, &rpc.ServerConfig{
		Lookup: func(userName upspin.UserName) (upspin.PublicKey, error) {
			user, err := key.Lookup(userName)
			if err != nil {
				return "", err
			}
			return user.PublicKey, nil
		},
		Service: rpc.Service{
			Name: "Key",
			Methods: map[string]rpc.Method{
				"Lookup": s.Lookup,
				"Put":    s.Put,
			},
		},
	})
}

func (s *server) serverFor(session rpc.Session, reqBytes []byte, req pb.Message) (upspin.KeyServer, error) {
	if err := pb.Unmarshal(reqBytes, req); err != nil {
		return nil, err
	}
	svc, err := s.key.Dial(config.SetUserName(s.config, session.User()), s.key.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.KeyServer), nil
}

// Lookup implements proto.KeyServer, and does not do any authentication.
func (s *server) Lookup(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	// TODO(adg): Lookup should be accessible even to unauthenticated users.

	var req proto.KeyLookupRequest
	key, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	logfOnceInN(100, "Lookup %q", req.UserName)

	user, err := key.Lookup(upspin.UserName(req.UserName))
	if err != nil {
		logf("Lookup %q failed: %s", req.UserName, err)
		return &proto.KeyLookupResponse{Error: errors.MarshalError(err)}, nil
	}
	return &proto.KeyLookupResponse{User: proto.UserProto(user)}, nil
}

// Put implements proto.KeyServer.
func (s *server) Put(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.KeyPutRequest
	key, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf("Put %v", req)

	user := proto.UpspinUser(req.User)
	err = key.Put(user)
	if err != nil {
		op.log(err)
		return putError(err), nil
	}
	return &proto.KeyPutResponse{}, nil
}

func putError(err error) *proto.KeyPutResponse {
	return &proto.KeyPutResponse{Error: errors.MarshalError(err)}
}

// logOnceInN logs an operation probabilistically once for every n calls.
func logfOnceInN(n int, format string, args ...interface{}) {
	if n <= 1 || rand.Intn(n) == 0 {
		logf(format, args...)
	}
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Print("rpc/keyserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	log.Printf("%v failed: %v", op, err)
}
