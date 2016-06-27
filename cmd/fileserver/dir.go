// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Fileserver is a directory and store implementation that serves local files through an Upspin grpc interface.
package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/auth/grpcauth"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	gContext "golang.org/x/net/context"
)

// DirServer is a SecureServer that serves the local file system's directory structure as an upspin.Directory gRPC server.
type DirServer struct {
	context       *upspin.Context
	endpoint      upspin.Endpoint
	storeEndpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

var (
	// Empty structs we can allocate just once.
	putResponse       proto.DirectoryPutResponse
	deleteResponse    proto.DirectoryDeleteResponse
	configureResponse proto.ConfigureResponse
)

func NewDirServer(context *upspin.Context, storeEndpoint, endpoint upspin.Endpoint, server grpcauth.SecureServer) *DirServer {
	s := &DirServer{
		context:       context,
		endpoint:      endpoint,
		storeEndpoint: storeEndpoint,
		SecureServer:  server,
	}
	return s
}

func (s *DirServer) Run(errChan chan error) {
	proto.RegisterDirectoryServer(s.SecureServer.GRPCServer(), s)
	listener, err := net.Listen("tcp", colonPort(s.endpoint))
	if err != nil {
		errChan <- err
	}
	log.Printf("Serve Directory at %q", s.endpoint)
	errChan <- s.SecureServer.Serve(listener)
}

// Lookup implements upspin.Directory.
func (s *DirServer) Lookup(ctx gContext.Context, req *proto.DirectoryLookupRequest) (*proto.DirectoryLookupResponse, error) {
	log.Printf("Lookup %q", req.Name)

	parsed, err := path.Parse(upspin.PathName(req.Name))
	if err != nil {
		return s.errLookup(err)
	}
	// Verify that the user name in the path is the owner of this root.
	if parsed.User() != s.context.UserName {
		err = errors.E("Lookup", errors.Invalid, parsed.Path(), errors.Errorf("mismatched user name %q", parsed.User()))
		return s.errLookup(err)
	}
	data, err := s.entryBytes(*root + parsed.FilePath())
	if err != nil {
		return s.errLookup(err)
	}
	return &proto.DirectoryLookupResponse{
		Entry: data,
	}, nil
}

// errLookup returns an error for a Lookup.
func (s *DirServer) errLookup(err error) (*proto.DirectoryLookupResponse, error) {
	return &proto.DirectoryLookupResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// entryBytes returns the marshaled DirEntry for the named local file name.
func (s *DirServer) entryBytes(file string) ([]byte, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	attr := upspin.AttrNone
	if info.IsDir() {
		attr = upspin.AttrDirectory
	}
	if !strings.HasPrefix(file, *root) {
		return nil, errors.Str("internal error: not in root")
	}
	file = file[len(*root):]
	name := string(s.context.UserName) + "/" + file
	entry := upspin.DirEntry{
		Name: upspin.PathName(name),
		Location: upspin.Location{
			Endpoint:  s.storeEndpoint,
			Reference: upspin.Reference(name),
		},
		Metadata: upspin.Metadata{
			Attr:     attr,
			Sequence: 0,
			Size:     uint64(info.Size()),
			Time:     upspin.TimeFromGo(info.ModTime()),
			Packdata: []byte{byte(upspin.PlainPack)},
		},
	}
	return entry.Marshal()
}

// Put implements upspin.Directory.
func (s *DirServer) Put(ctx gContext.Context, req *proto.DirectoryPutRequest) (*proto.DirectoryPutResponse, error) {
	log.Printf("Put")

	err := errors.E("Put", errors.Permission, errors.Str("read-only name space"))
	return &proto.DirectoryPutResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// MakeDirectory implements upspin.Directory.
func (s *DirServer) MakeDirectory(ctx gContext.Context, req *proto.DirectoryMakeDirectoryRequest) (*proto.DirectoryMakeDirectoryResponse, error) {
	log.Printf("MakeDirectory %q", req.Name)

	err := errors.E("MakeDirectory", errors.Permission, errors.Str("read-only name space"))
	return &proto.DirectoryMakeDirectoryResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Glob implements upspin.Directory.
func (s *DirServer) Glob(ctx gContext.Context, req *proto.DirectoryGlobRequest) (*proto.DirectoryGlobResponse, error) {
	log.Printf("Glob %q", req.Pattern)

	parsed, err := path.Parse(upspin.PathName(req.Pattern))
	if err != nil {
		return s.errGlob(err)
	}
	// Verify that the user name in the path is the owner of this root.
	if parsed.User() != s.context.UserName {
		err = errors.E("Glob", errors.Invalid, parsed.Path(), errors.Errorf("mismatched user name %q", parsed.User()))
		return s.errGlob(err)
	}
	matches, err := filepath.Glob(*root + parsed.FilePath())
	if err != nil {
		return s.errGlob(err)
	}
	entries := make([][]byte, len(matches))
	for i, match := range matches {
		entries[i], err = s.entryBytes(match)
		if err != nil {
			return s.errGlob(err)
		}
	}
	return &proto.DirectoryGlobResponse{
		Entries: entries,
	}, nil
}

// errGlob returns an error for a Glob.
func (s *DirServer) errGlob(err error) (*proto.DirectoryGlobResponse, error) {
	return &proto.DirectoryGlobResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Delete implements upspin.Directory.
func (s *DirServer) Delete(ctx gContext.Context, req *proto.DirectoryDeleteRequest) (*proto.DirectoryDeleteResponse, error) {
	log.Printf("Delete %q", req.Name)

	err := errors.E("Delete", errors.Permission, errors.Str("read-only name space"))
	return &proto.DirectoryDeleteResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// WhichAccess implements upspin.Directory.
func (s *DirServer) WhichAccess(ctx gContext.Context, req *proto.DirectoryWhichAccessRequest) (*proto.DirectoryWhichAccessResponse, error) {
	log.Printf("WhichAccess %q", req.Name)

	err := errors.E("WhichAccess", errors.Invalid, errors.Str("unimplemented"))
	return &proto.DirectoryWhichAccessResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Configure implements upspin.Service
func (s *DirServer) Configure(ctx gContext.Context, req *proto.ConfigureRequest) (*proto.ConfigureResponse, error) {
	log.Printf("Configure %q", req.Options)

	err := errors.E("Configure", errors.Permission, errors.Str("unimplemented"))
	return &proto.ConfigureResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Endpoint implements upspin.Service
func (s *DirServer) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {
	log.Print("Endpoint")
	resp := &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(s.endpoint.Transport),
			NetAddr:   string(s.endpoint.NetAddr),
		},
	}
	return resp, nil
}
