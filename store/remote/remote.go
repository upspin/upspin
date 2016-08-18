// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess store server that uses RPC to
// connect to a remote store server.
package remote

import (
	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// dialContext contains the destination and authenticated user of the dial.
type dialContext struct {
	endpoint upspin.Endpoint
	userName upspin.UserName
}

// remote implements upspin.StoreServer.
type remote struct {
	*grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	ctx                         dialContext
	storeClient                 proto.StoreClient
}

var _ upspin.StoreServer = (*remote)(nil)

// Get implements upspin.StoreServer.Get.
func (r *remote) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return nil, nil, err
	}
	req := &proto.StoreGetRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Get(gCtx, req)
	if err != nil {
		return nil, nil, errors.E("Get", errors.IO, err)
	}
	r.LastActivity()
	if len(resp.Error) != 0 {
		return nil, nil, errors.UnmarshalError(resp.Error)
	}
	return resp.Data, proto.UpspinLocations(resp.Locations), nil
}

// Put implements upspin.StoreServer.Put.
// Directories are created with MakeDirectory.
func (r *remote) Put(data []byte) (upspin.Reference, error) {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return "", err
	}
	req := &proto.StorePutRequest{
		Data: data,
	}
	resp, err := r.storeClient.Put(gCtx, req)
	if err != nil {
		return "", errors.E("Put", errors.IO, err)
	}
	r.LastActivity()
	return upspin.Reference(resp.Reference), errors.UnmarshalError(resp.Error)
}

// Delete implements upspin.StoreServer.Delete.
func (r *remote) Delete(ref upspin.Reference) error {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return err
	}
	req := &proto.StoreDeleteRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Delete(gCtx, req)
	if err != nil {
		return errors.E("Delete", errors.IO, err)
	}
	r.LastActivity()
	return errors.UnmarshalError(resp.Error)
}

// Endpoint implements upspin.StoreServer.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

// Configure implements upspin.Service.
func (r *remote) Configure(options ...string) error {
	req := &proto.ConfigureRequest{
		Options: options,
	}
	resp, err := r.storeClient.Configure(gContext.Background(), req)
	if err != nil {
		return errors.E("Configure", errors.IO, err)
	}
	return errors.UnmarshalError(resp.Error)
}

// Dial implements upspin.Service.
func (*remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}

	var err error
	var authClient *grpcauth.AuthClientService
	ce := context.StoreCacheEndpoint()
	if ce.Transport != upspin.Unassigned {
		authClient, err = grpcauth.NewGRPCClient(context, ce.NetAddr, grpcauth.KeepAliveInterval, grpcauth.NoSecurity)
	} else {
		authClient, err = grpcauth.NewGRPCClient(context, e.NetAddr, grpcauth.KeepAliveInterval, grpcauth.Secure)
	}
	if err != nil {
		return nil, err
	}
	// The connection is closed when this service is released (see Bind.Release)
	storeClient := proto.NewStoreClient(authClient.GRPCConn())
	authClient.SetService(storeClient)

	if ce.Transport != upspin.Unassigned {
		// Configure the cache connection and confirm the user.
		serverUser, err := authClient.CacheConfigure(context, e)
		if err != nil {
			return nil, err
		}
		if serverUser != context.UserName() {
			return nil, errors.E("Dial", errors.Invalid, errors.Errorf("incorrect cache user: %s", string(serverUser)))
		}
	}

	r := &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName(),
		},
		storeClient: storeClient,
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterStoreServer(transport, r)
}
