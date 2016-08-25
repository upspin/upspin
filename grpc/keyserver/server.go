// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package keyserver is a wrapper for an upspin.KeyServer implementation
// that presents it as an authenticated GRPC service.
package keyserver

import (
	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	// Load required transports
	_ "upspin.io/key/transports"
)

// server is a SecureServer that talks to a KeyServer interface and serves gRPC requests.
type server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying keyserver implementation.
	key upspin.KeyServer

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func New(ctx upspin.Context, key upspin.KeyServer, ss grpcauth.SecureServer, addr upspin.NetAddr) proto.KeyServer {
	return &server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		key:          key,
		SecureServer: ss,
	}
}

// keyServerFor returns a KeyServer bound to the user specified in the context.
func (s *server) keyServerFor(ctx gContext.Context) (upspin.KeyServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := s.key.Dial(s.context.Copy().SetUserName(session.User()), s.key.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.KeyServer), nil
}

// Lookup implements proto.KeyServer, and does not do any authentication.
func (s *server) Lookup(ctx gContext.Context, req *proto.KeyLookupRequest) (*proto.KeyLookupResponse, error) {
	log.Printf("Lookup %q", req.UserName)

	user, err := s.key.Lookup(upspin.UserName(req.UserName))
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.UserName, err)
		return &proto.KeyLookupResponse{Error: errors.MarshalError(err)}, nil
	}
	return &proto.KeyLookupResponse{User: proto.UserProto(user)}, nil
}

func keyPutError(err error) *proto.KeyPutResponse {
	return &proto.KeyPutResponse{Error: errors.MarshalError(err)}
}

// Put implements proto.KeyServer.
func (s *server) Put(ctx gContext.Context, req *proto.KeyPutRequest) (*proto.KeyPutResponse, error) {
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

// Configure implements proto.KeyServer.
func (s *server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	key, err := s.keyServerFor(ctx)
	if err != nil {
		return nil, err
	}
	_, err = key.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
		return &proto.ConfigureResponse{Error: errors.MarshalError(err)}, nil
	}
	return nil, nil
}

// Endpoint implements proto.KeyServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
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
