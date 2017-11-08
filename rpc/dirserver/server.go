// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dirserver provides a wrapper for an upspin.DirServer implementation
// that presents it as an authenticated service.
package dirserver // import "upspin.io/rpc/dirserver"

import (
	"fmt"
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

	// The underlying dirserver implementation.
	dir upspin.DirServer
}

func New(cfg upspin.Config, dir upspin.DirServer, addr upspin.NetAddr) http.Handler {
	s := &server{
		config: cfg,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		dir: dir,
	}

	return rpc.NewServer(cfg, rpc.Service{
		Name: "Dir",
		Methods: map[string]rpc.Method{
			"Delete":      s.Delete,
			"Glob":        s.Glob,
			"Lookup":      s.Lookup,
			"Put":         s.Put,
			"WhichAccess": s.WhichAccess,
		},
		Streams: map[string]rpc.Stream{
			"Watch": s.Watch,
		},
	})
}

func (s *server) serverFor(session rpc.Session, reqBytes []byte, req pb.Message) (upspin.DirServer, error) {
	if err := pb.Unmarshal(reqBytes, req); err != nil {
		return nil, err
	}
	e := s.dir.Endpoint()
	if ep := session.ProxiedEndpoint(); ep.Transport != upspin.Unassigned {
		e = ep
	}
	svc, err := s.dir.Dial(config.SetUserName(s.config, session.User()), e)
	if err != nil {
		return nil, err
	}
	return svc.(upspin.DirServer), nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirLookupRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf(session, "Lookup(%q)", req.Name)

	return op.entryError(dir.Lookup(upspin.PathName(req.Name)))
}

// Put implements proto.DirServer.
func (s *server) Put(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirPutRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := logf(session, "Put(%q)", entry.Name)

	return op.entryError(dir.Put(entry))
}

// Glob implements proto.DirServer.
func (s *server) Glob(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirGlobRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf(session, "Glob(%q)", req.Pattern)

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
func (s *server) Watch(session rpc.Session, reqBytes []byte, done <-chan struct{}) (<-chan pb.Message, error) {
	var req proto.DirWatchRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf(session, "Watch(%q, %d)", req.Name, req.Sequence)

	events, err := dir.Watch(upspin.PathName(req.Name), req.Sequence, done)
	if err != nil {
		op.log(err)
		return nil, err
	}

	out := make(chan pb.Message)
	go func() {
		defer close(out)
		for e := range events {
			ep, err := proto.EventProto(&e)
			if err != nil {
				op.logf("error converting event to proto: %v", err)
				return
			}
			out <- ep
		}
	}()
	return out, nil
}

// Delete implements proto.DirServer.
func (s *server) Delete(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirDeleteRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf(session, "Delete(%q)", req.Name)

	return op.entryError(dir.Delete(upspin.PathName(req.Name)))
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(session rpc.Session, reqBytes []byte) (pb.Message, error) {
	var req proto.DirWhichAccessRequest
	dir, err := s.serverFor(session, reqBytes, &req)
	if err != nil {
		return nil, err
	}
	op := logf(session, "WhichAccess(%q)", req.Name)

	return op.entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

func logf(sess rpc.Session, format string, args ...interface{}) operation {
	op := fmt.Sprintf("rpc/dirserver: %q: dir.", sess.User())
	op += fmt.Sprintf(format, args...)
	log.Debug.Print(op)
	return operation(op)
}

type operation string

func (op operation) log(err error) {
	log.Debug.Printf("%s failed: %s", op, err)
}

func (op operation) logf(format string, args ...interface{}) {
	log.Debug.Printf("%s: "+format, append([]interface{}{op}, args...)...)
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
