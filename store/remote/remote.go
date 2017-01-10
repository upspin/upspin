// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess store server that uses RPC to
// connect to a remote store server.
package remote

import (
	"fmt"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/grpc/auth"
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
	auth.Client // For sessions, Ping, and Close.
	ctx         dialContext
	storeClient proto.StoreClient
}

var _ upspin.StoreServer = (*remote)(nil)

// Get implements upspin.StoreServer.Get.
func (r *remote) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	op := r.opf("Get", "%q", ref)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, nil, nil, op.error(err)
	}
	req := &proto.StoreGetRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Get(gCtx, req, callOpt)
	err = finishAuth(err)
	if err != nil {
		return nil, nil, nil, op.error(errors.IO, err)
	}
	if len(resp.Error) != 0 {
		return nil, nil, nil, errors.UnmarshalError(resp.Error)
	}
	return resp.Data, proto.UpspinRefdata(resp.Refdata), proto.UpspinLocations(resp.Locations), nil
}

// Put implements upspin.StoreServer.Put.
func (r *remote) Put(data []byte) (*upspin.Refdata, error) {
	op := r.opf("Put", "%v bytes", len(data))

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.StorePutRequest{
		Data: data,
	}
	resp, err := r.storeClient.Put(gCtx, req, callOpt)
	err = finishAuth(err)
	if err != nil {
		return nil, op.error(errors.IO, err)
	}
	return proto.UpspinRefdata(resp.Refdata), op.error(errors.UnmarshalError(resp.Error))
}

// Delete implements upspin.StoreServer.Delete.
func (r *remote) Delete(ref upspin.Reference) error {
	op := r.opf("Delete", "%q", ref)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return op.error(err)
	}
	req := &proto.StoreDeleteRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Delete(gCtx, req, callOpt)
	err = finishAuth(err)
	if err != nil {
		return op.error(errors.IO, err)
	}
	return op.error(errors.UnmarshalError(resp.Error))
}

// Endpoint implements upspin.StoreServer.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

func dialCache(op *operation, context upspin.Context, proxyFor upspin.Endpoint) upspin.Service {
	// Are we using a cache?
	ce := context.CacheEndpoint()
	if ce.Transport == upspin.Unassigned {
		return nil
	}

	// Call the cache. The cache is local so don't bother with TLS.
	authClient, err := auth.NewClient(context, ce.NetAddr, auth.KeepAliveInterval, auth.NoSecurity, proxyFor)
	if err != nil {
		// On error dial direct.
		op.error(errors.IO, err)
		return nil
	}

	// The connection is closed when this service is released (see Bind.Release).
	storeClient := proto.NewStoreClient(authClient.GRPCConn())
	authClient.SetService(storeClient)

	return &remote{
		Client: authClient,
		ctx: dialContext{
			endpoint: proxyFor,
			userName: context.UserName(),
		},
		storeClient: storeClient,
	}
}

// Dial implements upspin.Service.
func (r *remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	op := r.opf("Dial", "%q, %q", context.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, errors.Str("unrecognized transport"))
	}

	// First try a cache
	if svc := dialCache(op, context, e); svc != nil {
		return svc, nil
	}

	// Call the server directly.
	authClient, err := auth.NewClient(context, e.NetAddr, auth.KeepAliveInterval, auth.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	// The connection is closed when this service is released (see Bind.Release)
	storeClient := proto.NewStoreClient(authClient.GRPCConn())
	authClient.SetService(storeClient)

	return &remote{
		Client: authClient,
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

func (r *remote) opf(method string, format string, args ...interface{}) *operation {
	ep := r.ctx.endpoint.String()
	s := fmt.Sprintf("store/remote.%s(%q)", method, ep)
	op := &operation{s, fmt.Sprintf(format, args...)}
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
