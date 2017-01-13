// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dirserver provides a wrapper for an upspin.DirServer implementation
// that presents it as an authenticated service.
package dirserver

import (
	"fmt"
	"net/http"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/transport/auth"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

type server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying dirserver implementation.
	dir upspin.DirServer
}

func New(ctx upspin.Context, dir upspin.DirServer, addr upspin.NetAddr) http.Handler {
	s := &server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		dir: dir,
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
	})

}

func (s *server) serverFor(session auth.Session, reqBytes []byte, req pb.Message) (upspin.DirServer, error) {
	if err := pb.Unmarshal(reqBytes, req); err != nil {
		return nil, err
	}
	svc, err := s.dir.Dial(context.SetUserName(s.context, session.User()), s.dir.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.DirServer), nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirLookupRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf("Lookup %q", req.Name)

	return op.entryError(dir.Lookup(upspin.PathName(req.Name)))
}

// Put implements proto.DirServer.
func (s *server) Put(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirPutRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := logf("Put %q", entry.Name)

	return op.entryError(dir.Put(entry))
}

// Glob implements proto.DirServer.
func (s *server) Glob(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirGlobRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf("Glob %q", req.Pattern)

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

// Watch implements proto.Watch.
func (s *server) Watch(session auth.Session, reqBytes []byte) (pb.Message, error) {
	return nil, upspin.ErrNotSupported
}

// Delete implements proto.DirServer.
func (s *server) Delete(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirDeleteRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf("Delete %q", req.Name)

	return op.entryError(dir.Delete(upspin.PathName(req.Name)))
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(session auth.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirWhichAccessRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf("WhichAccess %q", req.Name)

	return op.entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Info.Print("transport/dirserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%s failed: %s", op, err)
}

func (op operation) logf(format string, args ...interface{}) {
	log.Printf("%s: "+format, append([]interface{}{op}, args...)...)
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
