// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dirserver provides a wrapper for an upspin.DirServer implementation
// that presents it as an authenticated GRPC service.
package dirserver

import (
	"fmt"

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
	op := infof("Lookup %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.Lookup(upspin.PathName(req.Name)))
}

// Put implements proto.DirServer.
func (s *server) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.EntryError, error) {
	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := infof("Put %q", entry.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.Put(entry))
}

// MakeDirectory implements proto.DirServer.
func (s *server) MakeDirectory(ctx gContext.Context, req *proto.DirMakeDirectoryRequest) (*proto.EntryError, error) {
	op := infof("MakeDirectory %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.MakeDirectory(upspin.PathName(req.Name)))
}

// Glob implements proto.DirServer.
func (s *server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
	op := infof("Glob %q", req.Pattern)

	dir, err := s.dirFor(ctx)
	if err != nil {
		op.log(err)
		return globError(err), nil
	}

	entries, globErr := dir.Glob(req.Pattern)
	if globErr != nil && globErr != upspin.ErrFollowLink {
		op.log(err)
		return globError(globErr), nil
	}
	// Fall through OK for ErrFollowLink.

	b, err := proto.DirEntryBytes(entries)
	if err != nil {
		op.log(err)
		return globError(err), nil
	}
	return &proto.EntriesError{
		Entries: b,
		Error:   errors.MarshalError(globErr),
	}, nil
}

func globError(err error) *proto.EntriesError {
	return &proto.EntriesError{Error: errors.MarshalError(err)}
}

// Delete implements proto.DirServer.
func (s *server) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.EntryError, error) {
	op := infof("Delete %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.Delete(upspin.PathName(req.Name)))
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	op := infof("WhichAccess %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

// Empty struct we can allocate just once.
var configureResponse proto.ConfigureResponse

// Configure implements proto.DirServer.
func (s *server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	op := infof("Configure %q", req.Options)

	dir, err := s.dirFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.ConfigureResponse{Error: errors.MarshalError(err)}, nil
	}
	name, err := dir.Configure(req.Options...)
	if err != nil {
		op.log(err)
	}
	return &proto.ConfigureResponse{
		UserName: string(name),
		Error:    errors.MarshalError(err),
	}, nil
}

// Endpoint implements proto.DirServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	return &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(s.endpoint.Transport),
			NetAddr:   string(s.endpoint.NetAddr),
		},
	}, nil
}

func infof(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Info.Print("grpc/dirserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	infof("%v failed: %v", op, err)
}

// entryError performs the common operation of converting a directory entry
// and error result pair into the corresponding protocol buffer.
func (op operation) entryError(entry *upspin.DirEntry, err error) (*proto.EntryError, error) {
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
