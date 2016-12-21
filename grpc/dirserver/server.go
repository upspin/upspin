// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dirserver provides a wrapper for an upspin.DirServer implementation
// that presents it as an authenticated GRPC service.
package dirserver

import (
	"fmt"
	"io"

	"upspin.io/auth/grpcauth"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
	"upspin.io/user"

	gContext "golang.org/x/net/context"
)

// server is a SecureServer that talks to a DirServer interface and serves GRPC requests.
type server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying dirserver implementation.
	dir upspin.DirServer

	// For session handling and the Ping GRPC method.
	grpcauth.Server
}

func New(ctx upspin.Context, dir upspin.DirServer, authServer grpcauth.Server, addr upspin.NetAddr) proto.DirServer {
	return &server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   addr,
		},
		dir:    dir,
		Server: authServer,
	}
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(ctx gContext.Context) (upspin.DirServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.SessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	userName, err := user.Clean(session.User())
	if err != nil {
		return nil, err
	}
	svc, err := s.dir.Dial(context.SetUserName(s.context, userName), s.dir.Endpoint())
	if err != nil {
		return nil, err
	}
	return svc.(upspin.DirServer), nil
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

// Watch implements proto.Watch.
func (s *server) Watch(stream proto.Dir_WatchServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}

	op := logf("Watch %q %d", req.Name, req.Order)

	dir, err := s.dirFor(stream.Context())
	if err != nil {
		// Report error back to client.
		protoEvent := &proto.Event{
			Error: errors.MarshalError(err),
		}
		err = stream.Send(protoEvent)
		if err != nil {
			return err
		}
		return nil
	}

	done := make(chan struct{})
	events, watchErr := dir.Watch(upspin.PathName(req.Name), req.Order, done)

	// We must send the first error on the GRPC event channel, even if it's
	// nil, to indicate whether dir.Watch succeeded.
	protoEvent := &proto.Event{
		Error: errors.MarshalError(watchErr),
	}
	err = stream.Send(protoEvent)
	if err != nil {
		return err
	}
	if watchErr != nil {
		// We reported the error successfully and now this RPC is done.
		return nil
	}

	// The watcher was set up properly. We now watch events on the events
	// channel and send them to the client on the GRPC pipe. We also watch
	// the client's end of GRPC, and if it's closed, we terminate and
	// release all resources.

	go func() {
		// Block until the client closes the GRPC channel or until
		// we close our stream below (which may be caused by a timeout
		// or DirServer errors).
		_, err := stream.Recv()
		if err != nil && err != io.EOF {
			op.logf("error receiving from client: %s", err)
		}
		// By closing the done channel we tell both dir.Watch and the
		// goroutine below that the client is done.
		close(done)
	}()

	for {
		select {
		case e, ok := <-events:
			if !ok {
				// DirServer closed its event channel. Close
				// ours too.
				return nil
			}
			protoEvent, err := proto.EventProto(&e)
			if err != nil {
				// Conversion failed. Make a protoEvent error by
				// hand and send it.
				protoEvent = &proto.Event{
					Error: errors.MarshalError(err),
				}
			}
			err = stream.Send(protoEvent)
			if err != nil {
				// Send failed. Log error and fail.
				op.log(err)
				return err
			}
		case <-done:
			return nil
		}
	}
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
	return &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(s.endpoint.Transport),
			NetAddr:   string(s.endpoint.NetAddr),
		},
	}, nil
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Info.Print("grpc/dirserver: " + s)
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
