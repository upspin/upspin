// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess key server that uses RPC to
// connect to a remote key server.
package remote

import (
	"fmt"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/key/usercache"
	"upspin.io/log"
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
	op := opf("Lookup", "%q", name)

	req := &proto.KeyLookupRequest{
		UserName: string(name),
	}
	resp, err := r.keyClient.Lookup(gContext.Background(), req)
	if err != nil {
		return nil, op.error(errors.IO, err)
	}
	if len(resp.Error) != 0 {
		return nil, op.error(errors.UnmarshalError(resp.Error))
	}
	return proto.UpspinUser(resp.User), nil
}

func userName(user *upspin.User) string {
	if user == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", user.Name)
}

// Put implements upspin.Key.Put.
func (r *remote) Put(user *upspin.User) error {
	op := opf("Put", "%v", userName(user))

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return op.error(err)
	}
	req := &proto.KeyPutRequest{
		User: proto.UserProto(user),
	}
	resp, err := r.keyClient.Put(gCtx, req, callOpt)
	err = finishAuth(err)
	if err != nil {
		return op.error(errors.IO, err)
	}
	if len(resp.Error) != 0 {
		return op.error(errors.UnmarshalError(resp.Error))
	}
	return nil
}

// Endpoint implements upspin.StoreServer.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

// Configure implements upspin.Service.
func (r *remote) Configure(options ...string) (upspin.UserName, error) {
	op := opf("Configure", "%v", options)

	req := &proto.ConfigureRequest{
		Options: options,
	}
	resp, err := r.keyClient.Configure(gContext.Background(), req)
	if err != nil {
		return "", op.error(errors.IO, err)
	}
	return "", op.error(errors.UnmarshalError(resp.Error))
}

// Dial implements upspin.Service.
func (*remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	op := opf("Dial", "%q, %q", context.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, errors.Str("unrecognized transport"))
	}

	authClient, err := grpcauth.NewGRPCClient(context, e.NetAddr, grpcauth.KeepAliveInterval, grpcauth.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, e, err)
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
	bind.RegisterKeyServer(transport, usercache.Global(r))
}

func opf(method string, format string, args ...interface{}) *operation {
	op := &operation{"key/remote." + method, fmt.Sprintf(format, args...)}
	log.Debug.Print(op)
	return op
}

type operation struct {
	op   string
	args string
}

func (op *operation) String() string {
	return fmt.Sprintf("%s(%s)", op.op, op.args)
}

func (op *operation) error(args ...interface{}) error {
	if len(args) == 0 {
		panic("error called with zero args")
	}
	if len(args) == 1 {
		if e, ok := args[0].(error); ok && e == upspin.ErrFollowLink {
			return e
		}
		if args[0] == nil {
			return nil
		}
	}
	log.Debug.Printf("%v error: %v", op, errors.E(args...))
	return errors.E(append([]interface{}{op.op}, args...)...)
}
