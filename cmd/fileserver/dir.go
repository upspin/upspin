// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"

	"upspin.io/access"
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
		return errLookup(err)
	}
	if err := s.verifyUserRoot(parsed); err != nil {
		return errLookup(err)
	}
	if ok, err := can(s, ctx, access.List, parsed); err != nil {
		return errLookup(err)
	} else if !ok {
		return errLookup(access.ErrPermissionDenied)
	}

	data, err := s.entryBytes(*root + parsed.FilePath())
	if err != nil {
		return errLookup(err)
	}
	return &proto.DirLookupResponse{
		Entry: data,
	}, nil
}

// errLookup returns an error for a Lookup.
func errLookup(err error) (*proto.DirLookupResponse, error) {
	return &proto.DirLookupResponse{
		Error: errors.MarshalError(errors.E("Lookup", err)),
	}, nil
}

// entry returns the DirEntry for the named local file or directory.
func (s *DirServer) entry(file string) (*upspin.DirEntry, error) {
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
	entry := upspin.DirEntry{
		Name:     s.upspinPathFromLocal(file),
		Packing:  upspin.PlainPack,
		Time:     upspin.TimeFromGo(info.ModTime()),
		Attr:     attr,
		Sequence: 0,
		Writer:   s.context.UserName(), // TODO: Is there a better answer?
	}
	if !info.IsDir() {
		block := upspin.DirBlock{
			Location: upspin.Location{
				Endpoint:  s.endpoint,
				Reference: upspin.Reference(file),
			},
			Offset: 0,
			Size:   info.Size(),
		}
		entry.Blocks = []upspin.DirBlock{block}
	}
	return &entry, nil
}

// entry returns the marhsaled DirEntry for the named local file name.
func (s *DirServer) entryBytes(file string) ([]byte, error) {
	e, err := s.entry(file)
	if err != nil {
		return nil, err
	}

	return e.Marshal()
}

// upspinPathFromLocal returns the upspin.PathName for
// the given absolute local path name.
func (s *DirServer) upspinPathFromLocal(local string) upspin.PathName {
	return upspin.PathName(s.context.UserName()) + "/" + upspin.PathName(local[len(*root):])
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
	if err := s.verifyUserRoot(parsed); err != nil {
		return errGlob(err)
	}

	var (
		matches []string
		next    = []string{*root}
	)
	for i := 0; i < parsed.NElem(); i++ {
		elem := parsed.Elem(i)
		matches, next = next, matches[:0]
		for _, match := range matches {
			if isGlobPattern(elem) || i == parsed.NElem()-1 {
				parsed, err := path.Parse(s.upspinPathFromLocal(match))
				if err != nil {
					return errGlob(err)
				}
				if ok, err := can(s, ctx, access.List, parsed); err != nil {
					return errGlob(err)
				} else if !ok {
					continue
				}
			}
			names, err := filepath.Glob(filepath.Join(match, elem))
			// TODO(r): remove this error check
			if err != nil {
				return errGlob(err)
			}
			next = append(next, names...)
		}
	}
	matches = next

	entries := make([][]byte, len(matches))
	for i, match := range matches {
		e, err := s.entry(match)
		if err != nil {
			return errGlob(err)
		}
		parsed, err := path.Parse(upspin.PathName(s.upspinPathFromLocal(match)))
		if err != nil {
			return errGlob(err)
		}
		if ok, err := can(s, ctx, access.Read, parsed); err != nil {
			return errGlob(err)
		} else if !ok {
			e.Blocks = nil
			e.Packdata = nil
		}
		entries[i], err = e.Marshal()
		if err != nil {
			return errGlob(err)
		}
	}
	return &proto.DirGlobResponse{
		Entries: entries,
	}, nil
}

// isGlobPattern replies whether the given path element
// contains a glob pattern.
func isGlobPattern(elem string) bool {
	return strings.ContainsAny(elem, `*?[]`)
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
	if ok, err := can(s, ctx, access.AnyRight, parsed); err != nil {
		return errWhichAccess(err)
	} else if !ok {
		return errWhichAccess(access.ErrPermissionDenied)
	}
	accessPath, err := whichAccess(parsed)
	if err != nil {
		return errWhichAccess(err)
	}

	data, err := s.entryBytes(string(accessPath))
	if err != nil {
		return errWhichAccess(err)
	}
	return &proto.DirWhichAccessResponse{
		Entry: data,
	}, nil
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
