// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess key server that uses RPC to
// connect to a remote key server.
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

// remote implements upspin.KeyServer.
type remote struct {
	*grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	ctx                         dialContext
	keyClient                   proto.KeyClient
}

var _ upspin.KeyServer = (*remote)(nil)

// Lookup implements upspin.Key.Lookup.
func (r *remote) Lookup(name upspin.UserName) (*upspin.User, error) {
	req := &proto.KeyLookupRequest{
		UserName: string(name),
	}
	resp, err := r.keyClient.Lookup(gContext.Background(), req)
	if err != nil {
		return nil, errors.E("Lookup", errors.IO, err)
	}
	r.LastActivity()
	if len(resp.Error) != 0 {
		return nil, errors.UnmarshalError(resp.Error)
	}
	return proto.UpspinUser(resp.User), nil
}

// Put implements upspin.Key.Put.
func (r *remote) Put(user *upspin.User) error {
	gCtx, err := r.NewAuthContext()
	if err != nil {
		return err
	}
	req := &proto.KeyPutRequest{
		User: proto.UserProto(user),
	}
	resp, err := r.keyClient.Put(gCtx, req)
	if err != nil {
		return errors.E("Put", errors.IO, err)
	}
	r.LastActivity()
	if len(resp.Error) != 0 {
		return errors.UnmarshalError(resp.Error)
	}
	return nil
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
	resp, err := r.keyClient.Configure(gContext.Background(), req)
	if err != nil {
		return errors.E("Configure", errors.IO, err)
	}
	return errors.UnmarshalError(resp.Error)
}

// Dial implements upspin.Service.
func (*remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "Dial"
	if e.Transport != upspin.Remote {
		return nil, errors.E(op, errors.Invalid, errors.Str("unrecognized transport"))
	}

	authClient, err := grpcauth.NewGRPCClient(context, e.NetAddr, grpcauth.KeepAliveInterval, grpcauth.InsecureAllowingSelfSignedCertificates)
	if err != nil {
		return nil, errors.E(op, errors.IO, e, err)
	}

	// The connection is closed when this service is released (see Bind.Release)
	keyClient := proto.NewKeyClient(authClient.GRPCConn())
	authClient.SetService(keyClient)
	r := &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName(),
		},
		keyClient: keyClient,
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterKeyServer(transport, r)
}
