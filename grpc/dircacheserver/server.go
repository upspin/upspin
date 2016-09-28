// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dircacheserver is a caching proxy between a client and all directories.
// Cached entries are appended to a log to survive restarts.
package dircacheserver

import (
	"fmt"
	"os"
	"path"
	"time"

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
	ctx  upspin.Context
	clog clog

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

// New creates a new DirServer cache reading in the log and writing out a new compacted log.
func New(ctx upspin.Context, ss grpcauth.SecureServer) (proto.DirServer, error) {
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		homeDir = "/etc"
	}
	clog, err := openLog(ctx, path.Join(homeDir, "upspin/dircache"), 120*time.Second)
	if err != nil {
		return nil, err
	}
	return &server{
		ctx:          ctx,
		clog:         clog,
		SecureServer: ss,
	}, nil
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(ctx gContext.Context) (upspin.DirServer, *upspin.Endpoint, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, &upspin.Endpoint{}, err
	}
	e := session.ProxiedEndpoint()
	if e.Transport == upspin.Unassigned {
		return nil, &upspin.Endpoint{}, errors.Str("not yet configured")
	}
	dir, err := bind.DirServer(s.ctx, e)
	return dir, &e, err
}

// endpointFor returns a DirServer endpoint for the context.
func (s *server) endpointFor(ctx gContext.Context) (*upspin.Endpoint, error) {
	var ep upspin.Endpoint
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return &ep, err
	}
	ep = session.ProxiedEndpoint()
	if ep.Transport == upspin.Unassigned {
		return &ep, errors.Str("not yet configured")
	}
	return &ep, nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(ctx gContext.Context, req *proto.DirLookupRequest) (*proto.EntryError, error) {
	op := logf("Lookup %q", req.Name)

	dir, ep, err := s.dirFor(ctx)
	if err != nil {
		op.log(err)
		return op.entryError(nil, err)
	}

	name := upspin.PathName(req.Name)
	if e := s.clog.lookup(ep, name); e != nil {
		if len(e.entries) == 1 {
			return op.entryError(e.entries[0], e.error)
		}
		return nil, e.error
	}

	de, err := dir.Lookup(name)
	s.clog.logRequest(lookupReq, ep, name, err, de)

	return op.entryError(de, err)
}

// Glob implements proto.DirServer.
func (s *server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
	op := logf("Glob %q", req.Pattern)

	dir, ep, err := s.dirFor(ctx)
	if err != nil {
		op.log(err)
		return op.entriesError(nil, err)
	}

	name := upspin.PathName(req.Pattern)
	if e := s.clog.lookupGlob(ep, name); e != nil {
		return op.entriesError(e.entries, e.error)
	}

	entries, globReqErr := dir.Glob(req.Pattern)
	if plumbingError(globReqErr) {
		op.log(globReqErr)
		return op.entriesError(nil, globReqErr)
	}
	s.clog.logGlobRequest(ep, name, globReqErr, entries)

	return op.entriesError(entries, globReqErr)
}

// Put implements proto.DirServer.
// TODO(p): Remember access errors to avoid even trying?
func (s *server) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.EntryError, error) {
	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := logf("Put %q", entry.Name)

	dir, ep, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	de, err := dir.Put(entry)
	s.clog.logRequest(putReq, ep, entry.Name, err, de)

	return op.entryError(de, err)
}

// Delete implements proto.DirServer.
func (s *server) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.EntryError, error) {
	op := logf("Delete %q", req.Name)

	dir, ep, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	de, err := dir.Delete(upspin.PathName(req.Name))
	s.clog.logRequest(deleteReq, ep, upspin.PathName(req.Name), err, de)

	return op.entryError(de, err)
}

// WhichAccess implements proto.DirServer.
// TODO(p): figure out what to do here.
func (s *server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	op := logf("WhichAccess %q", req.Name)

	dir, _, err := s.dirFor(ctx)
	if err != nil {
		return op.entryError(nil, err)
	}

	return op.entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

// Endpoint implements proto.DirServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {

	op := logf("Endpoint")

	ep, err := s.endpointFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.EndpointResponse{}, err
	}
	return &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(ep.Transport),
			NetAddr:   string(ep.NetAddr),
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
	return &proto.EntryError{
		Entry: b,
		Error: errors.MarshalError(err),
	}, nil
}

// entriesError performs the common operation of converting a list of directory entries
// and error result pair into the corresponding protocol buffer.
func (op operation) entriesError(entries []*upspin.DirEntry, err error) (*proto.EntriesError, error) {
	var b [][]byte
	if entries != nil {
		var mErr error
		b, mErr = proto.DirEntryBytes(entries)
		if mErr != nil {
			return nil, mErr
		}
	}
	return &proto.EntriesError{
		Entries: b,
		Error:   errors.MarshalError(err),
	}, nil
}
