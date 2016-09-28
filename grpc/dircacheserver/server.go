// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dircacheserver is a caching proxy between a client and all directories.
// Cached entries are appended to a log to survive restarts.
package dircacheserver

import (
	"fmt"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	gContext "golang.org/x/net/context"
)

// server is a SecureServer that talks to a DirServer interface and serves gRPC requests.
type server struct {
	ctx upspin.Context

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

// New creates a new DirServer cache reading in and compacting the log.
// TODO(p): implement the log.
func New(ctx upspin.Context, ss grpcauth.SecureServer) (proto.DirServer, error) {
	return &server{
		ctx:          ctx,
		SecureServer: ss,
	}, nil
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(ctx gContext.Context) (upspin.DirServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	e := session.ProxiedEndpoint()
	if e.Transport == upspin.Unassigned {
		return nil, errors.Str("not yet configured")
	}
	return bind.DirServer(s.ctx, e)
}

// endpointFor returns a DirServer endpoint for the context.
func (s *server) endpointFor(ctx gContext.Context) (upspin.Endpoint, error) {
	var e upspin.Endpoint
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return e, err
	}
	e = session.ProxiedEndpoint()
	if e.Transport == upspin.Unassigned {
		return e, errors.Str("not yet configured")
	}
	return e, nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(ctx gContext.Context, req *proto.DirLookupRequest) (*proto.EntryError, error) {
	op := logf("Lookup %q", req.Name)

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
	op := logf("Put %q", entry.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.Put(entry))
}

// Glob implements proto.DirServer.
func (s *server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
	op := logf("Glob %q", req.Pattern)

	dir, err := s.dirFor(ctx)
	if err != nil {
		op.log(err)
		return globError(err), nil
	}

	entries, globErr := dir.Glob(req.Pattern)
	if globErr != nil && globErr != upspin.ErrFollowLink {
		op.log(globErr)
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
	op := logf("Delete %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.Delete(upspin.PathName(req.Name)))
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	op := logf("WhichAccess %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

// Endpoint implements proto.DirServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {

	op := logf("Endpoint")

	e, err := s.endpointFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.EndpointResponse{}, err
	}
	return &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(e.Transport),
			NetAddr:   string(e.NetAddr),
		},
	}, nil
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Info.Print("grpc/dircacheserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%v failed: %v", op, err)
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
