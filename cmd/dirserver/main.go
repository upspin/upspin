// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dirserver is a wrapper for a directory implementation that presents it as a grpc interface.
package main

import (
	"net/http"
	"os"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/dir/filesystem"
	"upspin.io/dir/gcp"
	"upspin.io/dir/inprocess"
	"upspin.io/dir/server"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	gContext "golang.org/x/net/context"

	// TODO: Which of these are actually needed?

	// Load useful packers
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"
)

// Server is a SecureServer that talks to a DirServer interface and serves gRPC requests.
type Server struct {
	context upspin.Context

	// What this server reports itself as through its Endpoint method.
	endpoint upspin.Endpoint

	// The underlying dirserver implementation.
	dir upspin.DirServer

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

const serverName = "dirserver"

func main() {
	flags.Parse("addr", "config", "context", "https", "kind", "log", "project")

	if flags.Project != "" {
		log.Connect(flags.Project, serverName)
		svr, err := metric.NewGCPSaver(flags.Project, "serverName", serverName)
		if err != nil {
			log.Fatalf("Can't start a metric saver for GCP project %q: %s", flags.Project, err)
		} else {
			metric.RegisterSaver(svr)
		}
	}

	// Load context and keys for this server. It needs a real upspin username and keys.
	ctxfd, err := os.Open(flags.Context)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	ctx, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}

	// Create a new store implementation.
	var dir upspin.DirServer
	err = nil
	switch flags.ServerKind {
	case "inprocess":
		dir = inprocess.New(ctx)
	case "gcp":
		dir, err = gcp.New(ctx, flags.Config...)
	case "filesystem":
		dir, err = filesystem.New(ctx, flags.Config...)
	case "dirserver":
		dir, err = server.New(ctx, flags.Config...)
	default:
		err = errors.Errorf("bad -kind %q", flags.ServerKind)
	}
	if err != nil {
		log.Fatalf("Setting up DirServer: %v", err)
	}

	config := auth.Config{Lookup: auth.PublicUserKeyService(ctx)}
	grpcSecureServer, err := grpcauth.NewSecureServer(config)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context: ctx,
		endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(flags.NetAddr),
		},
		dir:          dir,
		SecureServer: grpcSecureServer,
	}
	proto.RegisterDirServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	https.ListenAndServe(serverName, flags.HTTPSAddr, nil)
}

var (
	// Empty struct we can allocate just once.
	configureResponse proto.ConfigureResponse
)

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *Server) dirFor(ctx gContext.Context) (upspin.DirServer, error) {
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

// Lookup implements upspin.DirServer.
func (s *Server) Lookup(ctx gContext.Context, req *proto.DirLookupRequest) (*proto.EntryError, error) {
	log.Printf("Lookup %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.Lookup(upspin.PathName(req.Name)))
}

// Put implements upspin.DirServer.
func (s *Server) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.EntryError, error) {
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

// MakeDirectory implements upspin.DirServer.
func (s *Server) MakeDirectory(ctx gContext.Context, req *proto.DirMakeDirectoryRequest) (*proto.EntryError, error) {
	log.Printf("MakeDirectory %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.MakeDirectory(upspin.PathName(req.Name)))
}

// Glob implements upspin.DirServer.
func (s *Server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
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

// Delete implements upspin.DirServer.
func (s *Server) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.EntryError, error) {
	log.Printf("Delete %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.Delete(upspin.PathName(req.Name)))
}

// WhichAccess implements upspin.DirServer.
func (s *Server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	log.Printf("WhichAccess %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	return entryError(dir.WhichAccess(upspin.PathName(req.Name)))
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
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

// Endpoint implements upspin.Service
func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
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
