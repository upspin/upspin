// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dircacheserver is a caching proxy between a client and all directories.
// Cached entries are appended to a log to survive restarts.
package dircacheserver

import (
	"fmt"
	"net/http"
	ospath "path"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/transport/auth"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// server is a SecureServer that talks to a DirServer interface and serves requests.
type server struct {
	ctx  upspin.Context
	clog *clog
}

// New creates a new DirServer cache reading in the log and writing out a new compacted log.
func New(ctx upspin.Context, cacheDir string, maxLogBytes int64) (http.Handler, error) {
	clog, err := openLog(ctx, ospath.Join(cacheDir, "dircache"), maxLogBytes)
	if err != nil {
		return nil, err
	}
	s := &server{
		ctx:  ctx,
		clog: clog,
	}

	return auth.NewServer(ctx, &auth.ServerConfig{
		Service: auth.Service{
			Name: "Dir",
			Methods: auth.Methods{
				"Delete":      s.Delete,
				"Glob":        s.Glob,
				"Lookup":      s.Lookup,
				"Put":         s.Put,
				"Watch":       s.Watch,
				"WhichAccess": s.WhichAccess,
			},
		},
	}), nil
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(session auth.Session, path upspin.PathName) (upspin.DirServer, error) {
	ep := session.ProxiedEndpoint()
	if ep.Transport == upspin.Unassigned {
		return nil, errors.Str("not yet configured")
	}
	dir, err := bind.DirServer(s.ctx, ep)
	if err == nil {
		s.clog.proxyFor(path, &ep)
	}
	return dir, err
}

// endpointFor returns a DirServer endpoint for the context.
func (s *server) endpointFor(session auth.Session) (*upspin.Endpoint, error) {
	ep := session.ProxiedEndpoint()
	if ep.Transport == upspin.Unassigned {
		return &ep, errors.Str("not yet configured")
	}
	return &ep, nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirLookupRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	op := logf("Lookup %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(session, name)
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
func (s *server) Glob(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirGlobRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	op := logf("Glob %q", req.Pattern)

	name := path.Clean(upspin.PathName(req.Pattern))
	dir, err := s.dirFor(session, name)
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
func (s *server) Put(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirPutRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	entry, err := proto.UpspinDirEntry(req.Entry)
	entry.Name = path.Clean(entry.Name)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := logf("Put %q", entry.Name)

	dir, err := s.dirFor(session, entry.Name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	de, err := dir.Put(entry)
	s.clog.logRequest(putReq, entry.Name, err, de)

	return entryError(de, err)
}

// Delete implements proto.DirServer.
func (s *server) Delete(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirDeleteRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	op := logf("Delete %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(session, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	de, err := dir.Delete(name)
	s.clog.logRequest(deleteReq, name, err, de)

	return entryError(de, err)
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirWhichAccessRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	op := logf("WhichAccess %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(session, name)
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
func (s *server) Watch(session auth.Session, reqBytes []byte) (pb.Message, error) {
	return nil, upspin.ErrNotSupported
}

// Endpoint implements proto.DirServer.
func (s *server) Endpoint(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.EndpointRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	op := logf("Endpoint")

	ep, err := s.endpointFor(session)
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
	log.Debug.Print("transport/dircacheserver: " + s)
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
