// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess user server that uses RPC to
// connect to a remote user server.
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

// remote implements upspin.User.
type remote struct {
	grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	ctx                        dialContext
	userClient                 proto.UserClient
}

var _ upspin.User = (*remote)(nil)

// Lookup implements upspin.User.Lookup.
func (r *remote) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	req := &proto.UserLookupRequest{
		UserName: string(name),
	}
	resp, err := r.userClient.Lookup(gContext.Background(), req)
	if err != nil {
		return nil, nil, errors.E("Lookup", errors.IO, err)
	}
	if len(resp.Error) != 0 {
		return nil, nil, errors.UnmarshalError(resp.Error)
	}
	if len(resp.Endpoints) == 0 {
		resp.Endpoints = nil
	}
	if len(resp.PublicKeys) == 0 {
		resp.PublicKeys = nil
	}
	return proto.UpspinEndpoints(resp.Endpoints), proto.UpspinPublicKeys(resp.PublicKeys), nil
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
	resp, err := r.userClient.Configure(gContext.Background(), req)
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
	userClient := proto.NewUserClient(authClient.GRPCConn())
	authClient.SetService(userClient)
	if err != nil {
		return nil, err
	}
	r := &remote{
		AuthClientService: *authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName,
		},
		userClient: userClient,
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterUser(transport, r)
}
