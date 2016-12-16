// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dircacheserver is a caching proxy between a client and all directories.
// Cached entries are appended to a log to survive restarts.
package dircacheserver

import (
	"fmt"
	"os"
	ospath "path"
	"sync"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// server is a SecureServer that talks to a DirServer interface and serves GRPC requests.
type server struct {
	ctx  upspin.Context
	clog *clog

	// userToDirServerMapping is a mapping of users to directory server endpoints
	userToDirServerMapping *userToDirServerMapping

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

// New creates a new DirServer cache reading in the log and writing out a new compacted log.
func New(ctx upspin.Context, ss grpcauth.SecureServer) (proto.DirServer, error) {
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		return nil, errors.Str("$HOME not defined")
	}
	userToDirServerMapping := newUserToDirServerMapping()
	clog, err := openLog(ctx, ospath.Join(homeDir, "upspin/dircache"), 20*1024*1024, userToDirServerMapping)
	if err != nil {
		return nil, err
	}
	return &server{
		ctx:  ctx,
		clog: clog,
		userToDirServerMapping: userToDirServerMapping,
		SecureServer:           ss,
	}, nil
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(ctx gContext.Context, path upspin.PathName) (upspin.DirServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ep := session.ProxiedEndpoint()
	if ep.Transport == upspin.Unassigned {
		return nil, errors.Str("not yet configured")
	}
	dir, err := bind.DirServer(s.ctx, ep)
	if err == nil {
		s.userToDirServerMapping.Set(path, &ep)
	}
	return dir, err
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

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	if de, err, ok := s.clog.lookup(name); ok {
		return entryError(de, err)
	}

	de, err := dir.Lookup(name)
	s.clog.logRequest(lookupReq, name, err, de)

	return entryError(de, err)
}

// Glob implements proto.DirServer.
func (s *server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
	op := logf("Glob %q", req.Pattern)

	name := path.Clean(upspin.PathName(req.Pattern))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entriesError(nil, err)
	}

	if entries, err, ok := s.clog.lookupGlob(name); ok {
		return entriesError(entries, err)
	}

	entries, globReqErr := dir.Glob(string(name))
	s.clog.logGlobRequest(name, globReqErr, entries)

	return entriesError(entries, globReqErr)
}

// Put implements proto.DirServer.
// TODO(p): Remember access errors to avoid even trying?
func (s *server) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.EntryError, error) {
	entry, err := proto.UpspinDirEntry(req.Entry)
	entry.Name = path.Clean(entry.Name)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := logf("Put %q", entry.Name)

	dir, err := s.dirFor(ctx, entry.Name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	de, err := dir.Put(entry)
	s.clog.logRequest(putReq, entry.Name, err, de)

	return entryError(de, err)
}

// Delete implements proto.DirServer.
func (s *server) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.EntryError, error) {
	op := logf("Delete %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	de, err := dir.Delete(name)
	s.clog.logRequest(deleteReq, name, err, de)

	return entryError(de, err)
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	op := logf("WhichAccess %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	if de, ok := s.clog.whichAccess(name); ok {
		return entryError(de, nil)
	}
	de, err := dir.WhichAccess(name)
	s.clog.logRequest(whichAccessReq, name, err, de)

	return entryError(de, err)
}

// Watch implements proto.Watch.
func (s *server) Watch(stream proto.Dir_WatchServer) error {
	return stream.Send(&proto.Event{
		Error: errors.MarshalError(upspin.ErrNotSupported),
	})
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
	log.Debug.Print("grpc/dircacheserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%v failed: %v", op, err)
}

// entryError performs the common operation of converting a directory entry
// and error result pair into the corresponding protocol buffer.
func entryError(entry *upspin.DirEntry, err error) (*proto.EntryError, error) {
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
func entriesError(entries []*upspin.DirEntry, err error) (*proto.EntriesError, error) {
	if err != nil && err != upspin.ErrFollowLink {
		return globError(err), nil
	}
	// Fall through OK for ErrFollowLink.

	b, mErr := proto.DirEntryBytes(entries)
	if mErr != nil {
		return globError(mErr), nil
	}
	return &proto.EntriesError{
		Entries: b,
		Error:   errors.MarshalError(err),
	}, nil
}

func globError(err error) *proto.EntriesError {
	return &proto.EntriesError{Error: errors.MarshalError(err)}
}

// userToDirServerMapping is a cache from user name to the endpoing of its directory server.
type userToDirServerMapping struct {
	sync.Mutex
	m map[upspin.UserName]*upspin.Endpoint
}

func newUserToDirServerMapping() *userToDirServerMapping {
	return &userToDirServerMapping{m: make(map[upspin.UserName]*upspin.Endpoint)}
}

func (c *userToDirServerMapping) Set(p upspin.PathName, ep *upspin.Endpoint) {
	c.Lock()
	if parsed, err := path.Parse(p); err == nil {
		c.m[parsed.User()] = ep
	} else {
		log.Info.Printf("parse error on a cleaned name: %s", p)
	}
	c.Unlock()
}

func (c *userToDirServerMapping) Get(p upspin.PathName) *upspin.Endpoint {
	c.Lock()
	defer c.Unlock()
	if parsed, err := path.Parse(p); err == nil {
		return c.m[parsed.User()]
	}
	log.Info.Printf("parse error on a cleaned name: %s", p)
	return nil
}
