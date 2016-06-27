// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// requireAuthentication specifies whether the connection demands TLS.
const requireAuthentication = true

// dialContext contains the destination and authenticated user of the dial.
type dialContext struct {
	endpoint upspin.Endpoint
	userName upspin.UserName
}

// remote implements upspin.Directory.
type remote struct {
	upspin.NoConfiguration
	*grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	ctx                         dialContext
	dirClient                   proto.DirectoryClient
}

var _ upspin.Directory = (*remote)(nil)

// Glob implements upspin.Directory.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return nil, err
	}
	req := &proto.DirectoryGlobRequest{
		Pattern: pattern,
	}
	resp, err := r.dirClient.Glob(gCtx, req)
	if err != nil {
		return nil, errors.E("Glob", errors.IO, err)
	}
	if len(resp.Error) != 0 {
		return nil, errors.UnmarshalError(resp.Error)
	}
	if len(resp.Entries) == 0 {
		return nil, nil
	}
	return proto.UpspinDirEntries(resp.Entries)
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (r *remote) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return upspin.Location{}, err
	}
	req := &proto.DirectoryMakeDirectoryRequest{
		Name: string(directoryName),
	}
	resp, err := r.dirClient.MakeDirectory(gCtx, req)
	if err != nil {
		return upspin.Location{}, errors.E("MakeDirectory", errors.IO, err)
	}
	return proto.UpspinLocation(resp.Location), errors.UnmarshalError(resp.Error)
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (r *remote) Put(entry *upspin.DirEntry) error {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return err
	}
	b, err := entry.Marshal()
	if err != nil {
		return err
	}
	req := &proto.DirectoryPutRequest{
		Entry: b,
	}
	resp, err := r.dirClient.Put(gCtx, req)
	if err != nil {
		return errors.E("Put", errors.IO, err)
	}
	return errors.UnmarshalError(resp.Error)
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (r *remote) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return "", err
	}
	req := &proto.DirectoryWhichAccessRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.WhichAccess(gCtx, req)
	if err != nil {
		return "", errors.E("WhichAccess", errors.IO, err)
	}
	return upspin.PathName(resp.Name), errors.UnmarshalError(resp.Error)
}

// Delete implements upspin.Directory.Delete.
func (r *remote) Delete(pathName upspin.PathName) error {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return err
	}
	req := &proto.DirectoryDeleteRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.Delete(gCtx, req)
	if err != nil {
		return errors.E("Delete", errors.IO, err)
	}
	return errors.UnmarshalError(resp.Error)
}

// Lookup implements upspin.Directory.Lookup.
func (r *remote) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return nil, err
	}
	req := &proto.DirectoryLookupRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.Lookup(gCtx, req)
	if err != nil {
		return nil, errors.E("Lookup", errors.IO, err)
	}
	if len(resp.Error) != 0 {
		return nil, errors.UnmarshalError(resp.Error)
	}
	return proto.UpspinDirEntry(resp.Entry)
}

// Endpoint implements upspin.Store.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

// Configure implements upspin.Service.
func (r *remote) Configure(options ...string) error {
	req := &proto.ConfigureRequest{
		Options: options,
	}
	resp, err := r.dirClient.Configure(gContext.Background(), req)
	if err != nil {
		return errors.E("Configure", errors.IO, err)
	}
	return errors.UnmarshalError(resp.Error)
}

// Dial implements upspin.Service.
func (*remote) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}

	authClient, err := grpcauth.NewGRPCClient(context, e.NetAddr, grpcauth.KeepAliveInterval, grpcauth.AllowSelfSignedCertificate)
	if err != nil {
		return nil, err
	}
	// The connection is closed when this service is released (see Bind.Release)
	dirClient := proto.NewDirectoryClient(authClient.GRPCConn())
	authClient.SetService(dirClient)
	r := &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName,
		},
		dirClient: dirClient,
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterDirectory(transport, r)
}
