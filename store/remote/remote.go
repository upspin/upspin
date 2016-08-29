// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess store server that uses RPC to
// connect to a remote store server.
package remote

import (
	"fmt"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
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
	op := opf("Get", "%q", ref)

	gCtx, err := r.NewAuthContext()
	if err != nil {
		return nil, nil, op.error(err)
	}
	req := &proto.StoreGetRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Get(gCtx, req)
	if err != nil {
		return nil, nil, op.error(errors.IO, err)
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
	op := opf("Put", "%v bytes", len(data))

	gCtx, err := r.NewAuthContext()
	if err != nil {
		return "", op.error(err)
	}
	req := &proto.StorePutRequest{
		Data: data,
	}
	resp, err := r.storeClient.Put(gCtx, req)
	if err != nil {
		return "", op.error(errors.IO, err)
	}
	r.LastActivity()
	return upspin.Reference(resp.Reference), op.error(errors.UnmarshalError(resp.Error))
}

// Delete implements upspin.StoreServer.Delete.
func (r *remote) Delete(ref upspin.Reference) error {
	op := opf("Delete", "%q", ref)

	gCtx, err := r.NewAuthContext()
	if err != nil {
		return op.error(err)
	}
	req := &proto.StoreDeleteRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Delete(gCtx, req)
	if err != nil {
		return op.error(errors.IO, err)
	}
	r.LastActivity()
	return op.error(errors.UnmarshalError(resp.Error))
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
	resp, err := r.storeClient.Configure(gContext.Background(), req)
	if err != nil {
		return "", op.error(errors.IO, err)
	}
	return "", op.error(errors.UnmarshalError(resp.Error))
}

func dialCache(context upspin.Context, e upspin.Endpoint) upspin.Service {
	const op = "store/remote.dialCache"

	// Are we using a Store cache?
	ce := context.StoreCacheEndpoint()
	if ce.Transport == upspin.Unassigned {
		return nil
	}

	// Call the cache.  The cache is local so don't bother with TLS.
	authClient, err := grpcauth.NewGRPCClient(context, ce.NetAddr, grpcauth.KeepAliveInterval, grpcauth.NoSecurity)
	if err != nil {
		return nil
	}

	// The connection is closed when this service is released (see Bind.Release)
	storeClient := proto.NewStoreClient(authClient.GRPCConn())
	authClient.SetService(storeClient)

	// Configure the cache connection and confirm the user.
	serverUser, err := authClient.ConfigureProxy(context, e)
	if err != nil {
		return nil
	}
	if serverUser != context.UserName() {
		return nil
	}

	return &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName(),
		},
		storeClient: storeClient,
	}
}

// Dial implements upspin.Service.
func (*remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	op := opf("Dial", "%q, %q", context.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, errors.Str("unrecognized transport"))
	}

	// First try a cache
	r := dialCache(context, e)
	if r != nil {
		return r, nil
	}

	// Call the server directly.
	authClient, err := grpcauth.NewGRPCClient(context, e.NetAddr, grpcauth.KeepAliveInterval, grpcauth.Secure)
	if err != nil {
		return nil, op.error(errors.IO, e, err)
	}

	// The connection is closed when this service is released (see Bind.Release)
	storeClient := proto.NewStoreClient(authClient.GRPCConn())
	authClient.SetService(storeClient)

	return &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName(),
		},
		storeClient: storeClient,
	}, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterStoreServer(transport, r)
}

func opf(method string, format string, args ...interface{}) *operation {
	op := &operation{"store/remote." + method, fmt.Sprintf(format, args...)}
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
