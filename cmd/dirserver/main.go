// Dirserver is a wrapper for a directory implementation that presents it as a Go net/rpc interface.
// TODO: Switch to grpc one day.
package main

import (
	"flag"
	"fmt"
	"net"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/auth/grpcauth"
	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/upspin/proto"

	gContext "golang.org/x/net/context"

	// TODO: Which of these are actually needed?

	// Load useful packers
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"

	// Load required gcp services
	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"

	// Load required test services
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/testuser"

	// Load required remote services
	_ "upspin.googlesource.com/upspin.git/directory/remote"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/user/remote"
)

var (
	port         = flag.Int("port", 8081, "TCP port number")
	endpointFlag = flag.String("endpoint", "inprocess", "endpoint of remote service")
	noAuth       = flag.Bool("noauth", false, "Disable authentication.")
	certFile     = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	certKeyFile  = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

type Server struct {
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
	dir upspin.Directory
}

func main() {
	flag.Parse()
	log.Connect("google.com:upspin", "storeserver")

	endpoint, err := endpoint.Parse(*endpointFlag)
	if err != nil {
		log.Fatalf("endpoint parse error: %v", err)
	}

	// All we need in the context is some user name. It is unauthenticated. TODO?
	context := &upspin.Context{
		UserName: "dirserver",
	}

	dir, err := bind.Directory(context, *endpoint)
	if err != nil {
		log.Fatalf("binding to %q: %v", *endpoint, err)
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
		SecureServer: grpcSecureServer,
		dir:          dir,
	}

	proto.RegisterDirectoryServer(grpcSecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	grpcSecureServer.Serve(listener)
}

func (s *Server) Lookup(ctx gContext.Context, req *proto.DirectoryLookupRequest) (*proto.DirectoryLookupResponse, error) {
	log.Printf("Lookup %q", req.Name)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	entry, err := s.dir.Lookup(upspin.PathName(req.Name))
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

func (s *Server) Put(ctx gContext.Context, req *proto.DirectoryPutRequest) (*proto.DirectoryPutResponse, error) {
	log.Printf("Put")

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	entry, err := proto.UpspinDirEntry(req.Entry)
	if err != nil {
		return nil, err
	}
	log.Printf("Put %q", entry.Name)
	err = s.dir.Put(entry)
	if err != nil {
		log.Printf("Put %q failed: %v", entry.Name, err)
	}
	return nil, err
}

func (s *Server) MakeDirectory(ctx gContext.Context, req *proto.DirectoryMakeDirectoryRequest) (*proto.DirectoryMakeDirectoryResponse, error) {
	log.Printf("MakeDirectory %q", req.Name)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	loc, err := s.dir.MakeDirectory(upspin.PathName(req.Name))
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

func (s *Server) Glob(ctx gContext.Context, req *proto.DirectoryGlobRequest) (*proto.DirectoryGlobResponse, error) {
	log.Printf("Glob %q", req.Pattern)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := s.dir.Glob(req.Pattern)
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

func (s *Server) Delete(ctx gContext.Context, req *proto.DirectoryDeleteRequest) (*proto.DirectoryDeleteResponse, error) {
	log.Printf("Delete %q", req.Name)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	err = s.dir.Delete(upspin.PathName(req.Name))
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Name, err)
	}
	return nil, err
}

func (s *Server) WhichAccess(ctx gContext.Context, req *proto.DirectoryWhichAccessRequest) (*proto.DirectoryWhichAccessResponse, error) {
	log.Printf("WhichAccess %q", req.Name)

	// Validate that we have a session. If not, it's an auth error.
	_, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	name, err := s.dir.WhichAccess(upspin.PathName(req.Name))
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
	err := s.dir.Configure(req.Options...)
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return nil, err
}

// Endpoint implements upspin.Service
func (s *Server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")
	endpoint := s.dir.Endpoint()
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
	userName := s.dir.ServerUserName()
	resp := &proto.ServerUserNameResponse{
		UserName: string(userName),
	}
	return resp, nil
}
