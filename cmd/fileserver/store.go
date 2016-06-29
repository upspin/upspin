// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io/ioutil"
	"net"
	"os"

	"upspin.io/auth/grpcauth"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	gContext "golang.org/x/net/context"
)

// StoreServer is a SecureServer that serves the local files as an Upspin.Store gRPC server.
// Its references are simply the owner name followed by the path name: me@foo.com/etc/passwd.
type StoreServer struct {
	context  *upspin.Context
	endpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func NewStoreServer(context *upspin.Context, endpoint upspin.Endpoint, server grpcauth.SecureServer) *StoreServer {
	s := &StoreServer{
		context:      context,
		endpoint:     endpoint,
		SecureServer: server,
	}
	return s
}

func (s *StoreServer) Run(listener net.Listener, errChan chan error) {
	errChan <- s.SecureServer.Serve(listener)
}

// Get implements upspin.Store.
func (s *StoreServer) Get(ctx gContext.Context, req *proto.StoreGetRequest) (*proto.StoreGetResponse, error) {
	log.Printf("Get")

	ref := upspin.PathName(req.Reference)
	parsed, err := path.Parse(ref)
	if err != nil {
		return errGet(err)
	}
	// Verify that the user name in the path is the owner of this root.
	if parsed.User() != s.context.UserName {
		err = errors.E(errors.Invalid, parsed.Path(), errors.Errorf("mismatched user name %q", parsed.User()))
		return errGet(err)
	}
	// Is it a directory?
	localName := *root + parsed.FilePath()
	info, err := os.Stat(localName)
	if err != nil {
		return errGet(err)
	}
	if info.IsDir() {
		return errGet(errors.E(errors.IsDir, parsed.Path()))
	}
	// Symlinks are OK. TODO?

	data, err := ioutil.ReadFile(localName)
	if err != nil {
		return errGet(err)
	}
	return &proto.StoreGetResponse{
		Data: data,
	}, nil
}

// errGet returns an error for a Get.
func errGet(err error) (*proto.StoreGetResponse, error) {
	return &proto.StoreGetResponse{
		Error: errors.MarshalError(errors.E("Get", err)),
	}, nil
}

// Put implements upspin.Store.
func (s *StoreServer) Put(ctx gContext.Context, req *proto.StorePutRequest) (*proto.StorePutResponse, error) {
	log.Printf("Put")

	err := errors.E("Put", errors.Permission, errors.Str("read-only name space"))
	return &proto.StorePutResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Delete implements upspin.Store.
func (s *StoreServer) Delete(ctx gContext.Context, req *proto.StoreDeleteRequest) (*proto.StoreDeleteResponse, error) {
	log.Printf("Delete %q", req.Reference)

	err := errors.E("Delete", errors.Permission, errors.Str("read-only name space"))
	return &proto.StoreDeleteResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Configure implements upspin.Service
func (s *StoreServer) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	err := errors.E("Configure", errors.Permission, errors.Str("unimplemented"))
	return &proto.ConfigureResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Endpoint implements upspin.Service
func (s *StoreServer) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(s.endpoint.Transport),
			NetAddr:   string(s.endpoint.NetAddr),
		},
	}
	return resp, nil
}
