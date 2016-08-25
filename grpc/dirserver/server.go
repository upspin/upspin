// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dirserver provides a wrapper for an upspin.DirServer implementation
// that presents it as an authenticated GRPC service.
package dirserver

import (
	"upspin.io/auth/grpcauth"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	gContext "golang.org/x/net/context"
)

// server is a SecureServer that talks to a DirServer interface and serves gRPC requests.
type server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying dirserver implementation.
	dir upspin.DirServer

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func New(ctx upspin.Context, dir upspin.DirServer, ss grpcauth.SecureServer, addr upspin.NetAddr) proto.DirServer {
	return &server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		dir:          dir,
		SecureServer: ss,
	}
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(ctx gContext.Context) (upspin.DirServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := s.dir.Dial(s.context.Copy().SetUserName(session.User()), s.dir.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.DirServer), nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(ctx gContext.Context, req *proto.DirLookupRequest) (*proto.EntryError, error) {
	log.Printf("Lookup %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.Lookup(upspin.PathName(req.Name)))
}

// Put implements proto.DirServer.
func (s *server) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.EntryError, error) {
	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	log.Printf("Put %q", entry.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.Put(entry))
}

// MakeDirectory implements proto.DirServer.
func (s *server) MakeDirectory(ctx gContext.Context, req *proto.DirMakeDirectoryRequest) (*proto.EntryError, error) {
	log.Printf("MakeDirectory %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.MakeDirectory(upspin.PathName(req.Name)))
}

// Glob implements proto.DirServer.
func (s *server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
	log.Printf("Glob %q", req.Pattern)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	entries, globErr := dir.Glob(req.Pattern)
	if globErr != nil && globErr != upspin.ErrFollowLink {
		log.Printf("Glob %q failed: %v", req.Pattern, globErr)
		return &proto.EntriesError{Error: errors.MarshalError(globErr)}, nil
	}

	// Fall through OK for ErrFollowLink.
	b, err := proto.DirEntryBytes(entries)
	if err != nil {
		return nil, err
	}
	resp := &proto.EntriesError{
		Entries: b,
		Error:   errors.MarshalError(globErr),
	}
	return resp, err
}

// Delete implements proto.DirServer.
func (s *server) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.EntryError, error) {
	log.Printf("Delete %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.Delete(upspin.PathName(req.Name)))
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	log.Printf("WhichAccess %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

// Empty struct we can allocate just once.
var configureResponse proto.ConfigureResponse

// Configure implements proto.DirServer.
func (s *server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	_, err = dir.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return &configureResponse, err
}

// Endpoint implements proto.DirServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := dir.Endpoint()
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(endpoint.Transport),
			NetAddr:   string(endpoint.NetAddr),
		},
	}
	return resp, nil
}

// entryError performs the common operation of converting a directory entry
// and error result pair into the corresponding protocol buffer.
func entryError(entry *upspin.DirEntry, err error) (*proto.EntryError, error) {
	if err != nil {
		log.Println(err)
	}
	var b []byte
	if entry != nil {
		var mErr error
		b, mErr = entry.Marshal()
		if mErr != nil {
			return nil, mErr
		}
	}
	return &proto.EntryError{Entry: b, Error: errors.MarshalError(err)}, nil
}
