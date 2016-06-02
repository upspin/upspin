// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Dirserver is a wrapper for a directory implementation that presents it as a Go net/rpc interface.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/endpoint"
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
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"
)

var (
	port         = flag.Int("port", 8081, "TCP port number")
	ctxfile      = flag.String("context", os.Getenv("HOME")+"/upspin/rc.dirserver", "context file to use to configure server")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	noAuth       = flag.Bool("noauth", false, "Disable authentication.")
	certFile     = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	certKeyFile  = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

// Server is a SecureServer that talks to a Directory interface and serves gRPC requests.
type Server struct {
	context  *upspin.Context
	endpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

func main() {
	flag.Parse()
	log.Connect("google.com:upspin", "storeserver")

	if *noAuth {
		*certFile = ""
		*certKeyFile = ""
	}

	ctxfd, err := os.Open(*ctxfile)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	context, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}

	endpoint, err := endpoint.Parse(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	config := auth.Config{
		Lookup: auth.PublicUserKeyService(),
		AllowUnauthenticatedConnections: *noAuth,
	}
	grpcSecureServer, err := grpcauth.NewSecureServer(config, *certFile, *certKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context:      context,
		SecureServer: grpcSecureServer,
		endpoint:     *endpoint,
	}

	proto.RegisterDirectoryServer(grpcSecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcSecureServer.Serve(listener)
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
		return nil, err
	}
	b, err := entry.Marshal()
	if err != nil {
		return nil, err
	}

	resp := &proto.DirectoryLookupResponse{
		Entry: b,
	}
	return resp, err
}

// Put implements upspin.Directory.
func (s *Server) Put(ctx gContext.Context, req *proto.DirectoryPutRequest) (*proto.DirectoryPutResponse, error) {
	log.Printf("Put")

	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return nil, err
	}
	log.Printf("Put %q", entry.Name)
	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	err = dir.Put(entry)
	if err != nil {
		log.Printf("Put %q failed: %v", entry.Name, err)
	}
	return &putResponse, err
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
		return nil, err
	}
	locSlice := []upspin.Location{loc}
	resp := &proto.DirectoryMakeDirectoryResponse{
		Location: proto.Locations(locSlice)[0], // Clumsy but easy (and rare).
	}
	return resp, err
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
		return nil, err
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
	}
	return &deleteResponse, err
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
		Name: string(name),
	}
	return resp, err
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

// ServerUserName implements upspin.Service
func (s *Server) ServerUserName(ctx gContext.Context, req *proto.ServerUserNameRequest) (*proto.ServerUserNameResponse, error) {
	log.Print("ServerUserName")
	dir, err := s.dirFor(ctx)
	if err != nil {
		return nil, err
	}
	userName := dir.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}
