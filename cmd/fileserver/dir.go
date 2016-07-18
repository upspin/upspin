// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	goPath "path"
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

// DirServer is a SecureServer that serves the local file system's directory structure as an upspin.DirServer gRPC server.
type DirServer struct {
	context  upspin.Context
	endpoint upspin.Endpoint
	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

var (
	// Empty structs we can allocate just once.
	putResponse       proto.DirPutResponse
	deleteResponse    proto.DirDeleteResponse
	configureResponse proto.ConfigureResponse
)

func NewDirServer(context upspin.Context, endpoint upspin.Endpoint, server grpcauth.SecureServer) *DirServer {
	s := &DirServer{
		context:      context,
		endpoint:     endpoint,
		SecureServer: server,
	}
	return s
}

// verifyUserRoot checks that the user name in the path is the owner of this root.
func (s *DirServer) verifyUserRoot(parsed path.Parsed) error {
	if parsed.User() != s.context.UserName() {
		return errors.E(errors.Invalid, parsed.Path(), errors.Errorf("mismatched user name %q", parsed.User()))
	}
	return nil
}

// Lookup implements upspin.DirServer.
func (s *DirServer) Lookup(ctx gContext.Context, req *proto.DirLookupRequest) (*proto.DirLookupResponse, error) {
	log.Printf("Lookup %q", req.Name)

	parsed, err := path.Parse(upspin.PathName(req.Name))
	if err != nil {
		return s.errLookup(err)
	}
	err = s.verifyUserRoot(parsed)
	if err != nil {
		return s.errLookup(err)
	}
	data, err := s.entryBytes(*root + parsed.FilePath())
	if err != nil {
		return s.errLookup(err)
	}
	return &proto.DirLookupResponse{
		Entry: data,
	}, nil
}

// errLookup returns an error for a Lookup.
func (s *DirServer) errLookup(err error) (*proto.DirLookupResponse, error) {
	return &proto.DirLookupResponse{
		Error: errors.MarshalError(errors.E("Lookup", err)),
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
	name := string(s.context.UserName()) + "/" + file
	entry := upspin.DirEntry{
		Name: upspin.PathName(name),
		Location: upspin.Location{
			Endpoint:  s.endpoint,
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

// Put implements upspin.DirServer.
func (s *DirServer) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.DirPutResponse, error) {
	var entry upspin.DirEntry
	_, err := entry.Unmarshal(req.Entry)
	if err != nil {
		return &proto.DirPutResponse{
			Error: errors.MarshalError(errors.E("Put", err)),
		}, nil
	}
	log.Printf("Put %s", entry.Name)

	err = errors.E("Put", errors.Permission, errors.Str("read-only name space"))
	return &proto.DirPutResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// MakeDirectory implements upspin.DirServer.
func (s *DirServer) MakeDirectory(ctx gContext.Context, req *proto.DirMakeDirectoryRequest) (*proto.DirMakeDirectoryResponse, error) {
	log.Printf("MakeDirectory %q", req.Name)

	err := errors.E("MakeDirectory", errors.Permission, errors.Str("read-only name space"))
	return &proto.DirMakeDirectoryResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// Glob implements upspin.DirServer.
func (s *DirServer) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.DirGlobResponse, error) {
	log.Printf("Glob %q", req.Pattern)

	parsed, err := path.Parse(upspin.PathName(req.Pattern))
	if err != nil {
		return errGlob(err)
	}
	// Verify that the user name in the path is the owner of this root.
	if parsed.User() != s.context.UserName() {
		err = errors.E(errors.Invalid, parsed.Path(), errors.Errorf("mismatched user name %q", parsed.User()))
		return errGlob(err)
	}
	matches, err := filepath.Glob(*root + parsed.FilePath())
	if err != nil {
		return errGlob(err)
	}
	entries := make([][]byte, len(matches))
	for i, match := range matches {
		entries[i], err = s.entryBytes(match)
		if err != nil {
			return errGlob(err)
		}
	}
	return &proto.DirGlobResponse{
		Entries: entries,
	}, nil
}

// errGlob returns an error for a Glob.
func errGlob(err error) (*proto.DirGlobResponse, error) {
	return &proto.DirGlobResponse{
		Error: errors.MarshalError(errors.E("Glob", err)),
	}, nil
}

// Delete implements upspin.DirServer.
func (s *DirServer) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.DirDeleteResponse, error) {
	log.Printf("Delete %q", req.Name)

	err := errors.E("Delete", errors.Permission, errors.Str("read-only name space"))
	return &proto.DirDeleteResponse{
		Error: errors.MarshalError(err),
	}, nil
}

// WhichAccess implements upspin.DirServer.
func (s *DirServer) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.DirWhichAccessResponse, error) {
	log.Printf("WhichAccess %q", req.Name)

	parsed, err := path.Parse(upspin.PathName(req.Name))
	if err != nil {
		return errWhichAccess(err)
	}
	err = s.verifyUserRoot(parsed)
	if err != nil {
		return errWhichAccess(err)
	}
	accessPath, err := whichAccess(parsed)
	if err != nil {
		return errWhichAccess(err)
	}

	return &proto.DirWhichAccessResponse{
		Name: string(accessPath),
	}, nil
}

// whichAccess is the core of the WhichAccess method, factored out so
// it can be called from other locations.
func whichAccess(parsed path.Parsed) (upspin.PathName, error) {
	// Look for Access file starting at end of local path.
	for i := 0; i < parsed.NElem(); i++ {
		name := filepath.Join(*root, filepath.FromSlash(parsed.Drop(i).FilePath()), "Access")
		fi, err := os.Stat(name)
		// Must exist and be a plain file.
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		// File exists. Is it a regular file?
		accessFile := goPath.Join(parsed.Drop(i).String(), "Access")
		if !fi.Mode().IsRegular() {
			return "", errors.Errorf("%q is not a regular file", accessFile)
		}
		fd, err := os.Open(name)
		if err != nil {
			// File exists but cannot be read.
			return "", err
		}
		fd.Close()
		return upspin.PathName(accessFile), nil

	}
	return "", nil
}

// errWhichAccess returns an error for a WhichAccess.
func errWhichAccess(err error) (*proto.DirWhichAccessResponse, error) {
	return &proto.DirWhichAccessResponse{
		Error: errors.MarshalError(errors.E("WhichAccess", err)),
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
