// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dirserver is a wrapper for a directory implementation that presents it as a grpc interface.
package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	gContext "golang.org/x/net/context"

	// TODO: Which of these are actually needed?

	// Load useful packers
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/directory/transports"
	"upspin.io/metric"
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"
)

var (
	httpsAddr    = flag.String("https_addr", "localhost:8000", "HTTPS listen address")
	ctxfile      = flag.String("context", filepath.Join(os.Getenv("HOME"), "/upspin/rc.dirserver"), "context file to use to configure server")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	config       = flag.String("config", "", "Comma-separated list of configuration options for this server")
	logFile      = flag.String("logfile", "dirserver", "Name of the log file on GCP or empty for no GCP logging")
)

// Server is a SecureServer that talks to a Directory interface and serves gRPC requests.
type Server struct {
	context  *upspin.Context
	endpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

const upspinProject = "google.com:upspin"

func main() {
	flag.Parse()

	if *logFile != "" {
		log.Connect(upspinProject, *logFile)
	}

	svr, err := metric.NewGCPSaver(upspinProject)
	if err != nil {
		log.Error.Printf("Can't start a metric saver for GCP project upspin. No metrics will be saved")
	} else {
		metric.RegisterSaver(svr)
	}

	// Load context and keys for this server. It needs a real upspin username and keys.
	ctxfd, err := os.Open(*ctxfile)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	context, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}

	endpoint, err := upspin.ParseEndpoint(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// Get an instance so we can configure it and use it for authenticated connections.
	dir, err := bind.Directory(context, *endpoint)
	if err != nil {
		log.Fatal(err)
	}

	// If there are configuration options, set them now.
	if *config != "" {
		opts := strings.Split(*config, ",")
		// Configure it appropriately.
		log.Printf("Configuring server with options: %v", opts)
		err = dir.Configure(opts...)
		if err != nil {
			log.Fatal(err)
		}
		// Now this pre-configured Directory is the one that will generate new instances.
		err = bind.ReregisterDirectory(endpoint.Transport, dir)
		if err != nil {
			log.Fatal(err)
		}
	}

	config := auth.Config{Lookup: auth.PublicUserKeyService()}
	grpcSecureServer, err := grpcauth.NewSecureServer(config)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context:      context,
		SecureServer: grpcSecureServer,
		endpoint:     *endpoint,
	}
	proto.RegisterDirectoryServer(grpcSecureServer.GRPCServer(), s)

	http.Handle("/", grpcSecureServer.GRPCServer())
	https.ListenAndServe("dirserver", *httpsAddr, nil)
}

var (
	// Empty structs we can allocate just once.
	putResponse       proto.DirectoryPutResponse
	deleteResponse    proto.DirectoryDeleteResponse
	configureResponse proto.ConfigureResponse
)

// dirFor returns a Directory service bound to the user specified in the context.
func (s *Server) dirFor(ctx gContext.Context) (upspin.Directory, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	context := *s.context
	context.UserName = session.User()
	return bind.Directory(&context, s.endpoint)
}

// Lookup implements upspin.Directory.
func (s *Server) Lookup(ctx gContext.Context, req *proto.DirectoryLookupRequest) (*proto.DirectoryLookupResponse, error) {
	log.Printf("Lookup %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	entry, err := dir.Lookup(upspin.PathName(req.Name))
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.Name, err)
		return &proto.DirectoryLookupResponse{Error: errors.MarshalError(err)}, nil
	}
	b, err := entry.Marshal()
	if err != nil {
		return nil, err
	}

	resp := &proto.DirectoryLookupResponse{
		Entry: b,
	}
	return resp, nil
}

// Put implements upspin.Directory.
func (s *Server) Put(ctx gContext.Context, req *proto.DirectoryPutRequest) (*proto.DirectoryPutResponse, error) {
	log.Printf("Put")

	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return &proto.DirectoryPutResponse{Error: errors.MarshalError(err)}, nil
	}
	log.Printf("Put %q", entry.Name)
	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	err = dir.Put(entry)
	if err != nil {
		log.Printf("Put %q failed: %v", entry.Name, err)
		return &proto.DirectoryPutResponse{Error: errors.MarshalError(err)}, nil
	}
	return &putResponse, nil
}

// MakeDirectory implements upspin.Directory.
func (s *Server) MakeDirectory(ctx gContext.Context, req *proto.DirectoryMakeDirectoryRequest) (*proto.DirectoryMakeDirectoryResponse, error) {
	log.Printf("MakeDirectory %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	loc, err := dir.MakeDirectory(upspin.PathName(req.Name))
	if err != nil {
		log.Printf("MakeDirectory %q failed: %v", req.Name, err)
		return &proto.DirectoryMakeDirectoryResponse{Error: errors.MarshalError(err)}, nil
	}
	locSlice := []upspin.Location{loc}
	resp := &proto.DirectoryMakeDirectoryResponse{
		Location: proto.Locations(locSlice)[0], // Clumsy but easy (and rare).
	}
	return resp, nil
}

// Glob implements upspin.Directory.
func (s *Server) Glob(ctx gContext.Context, req *proto.DirectoryGlobRequest) (*proto.DirectoryGlobResponse, error) {
	log.Printf("Glob %q", req.Pattern)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := dir.Glob(req.Pattern)
	if err != nil {
		log.Printf("Glob %q failed: %v", req.Pattern, err)
		return &proto.DirectoryGlobResponse{Error: errors.MarshalError(err)}, nil
	}
	data, err := proto.DirEntryBytes(entries)
	resp := &proto.DirectoryGlobResponse{
		Entries: data,
	}
	return resp, err
}

// Delete implements upspin.Directory.
func (s *Server) Delete(ctx gContext.Context, req *proto.DirectoryDeleteRequest) (*proto.DirectoryDeleteResponse, error) {
	log.Printf("Delete %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	err = dir.Delete(upspin.PathName(req.Name))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Name, err)
		return &proto.DirectoryDeleteResponse{Error: errors.MarshalError(err)}, nil
	}
	return &deleteResponse, nil
}

// WhichAccess implements upspin.Directory.
func (s *Server) WhichAccess(ctx gContext.Context, req *proto.DirectoryWhichAccessRequest) (*proto.DirectoryWhichAccessResponse, error) {
	log.Printf("WhichAccess %q", req.Name)

	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	name, err := dir.WhichAccess(upspin.PathName(req.Name))
	if err != nil {
		log.Printf("WhichAccess %q failed: %v", req.Name, err)
	}
	resp := &proto.DirectoryWhichAccessResponse{
		Error: errors.MarshalError(err),
		Name:  string(name),
	}
	return resp, nil
}

// Configure implements upspin.Service
func (s *Server) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)
	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	err = dir.Configure(req.Options...)
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
