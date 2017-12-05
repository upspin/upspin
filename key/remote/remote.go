// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess key server that uses RPC to
// connect to a remote key server.
package remote // import "upspin.io/key/remote"

import (
	"fmt"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/key/usercache"
	"upspin.io/log"
	"upspin.io/rpc"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// dialConfig contains the destination and authenticated user of the dial.
type dialConfig struct {
	endpoint upspin.Endpoint
	userName upspin.UserName
}

// remote implements upspin.KeyServer.
type remote struct {
	rpc.Client // For sessions and Close.
	cfg        dialConfig
}

var _ upspin.KeyServer = (*remote)(nil)

// Lookup implements upspin.Key.Lookup.
func (r *remote) Lookup(name upspin.UserName) (*upspin.User, error) {
	op := r.opf("Lookup", "%q", name)
	// TODO(adg): don't send auth requests when performing lookups.

	req := &proto.KeyLookupRequest{
		UserName: string(name),
	}
	resp := new(proto.KeyLookupResponse)
	if err := r.InvokeUnauthenticated("Key/Lookup", req, resp); err != nil {
		return nil, op.error(err)
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
	op := r.opf("Put", "%v", userName(user))

	req := &proto.KeyPutRequest{
		User: proto.UserProto(user),
	}
	resp := new(proto.KeyPutResponse)
	if err := r.Invoke("Key/Put", req, resp, nil, nil); err != nil {
		return op.error(err)
	}
	if len(resp.Error) != 0 {
		return op.error(errors.UnmarshalError(resp.Error))
	}
	return nil
}

// Endpoint implements upspin.StoreServer.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.cfg.endpoint
}

// Dial implements upspin.Service.
func (r *remote) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	op := r.opf("Dial", "%q, %q", config.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, "unrecognized transport")
	}

	authClient, err := rpc.NewClient(config, e.NetAddr, rpc.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	return &remote{
		Client: authClient,
		cfg: dialConfig{
			endpoint: e,
			userName: config.UserName(),
		},
	}, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterKeyServer(transport, usercache.Global(r))
}

func (r *remote) opf(method string, format string, args ...interface{}) *operation {
	addr := r.cfg.endpoint.NetAddr
	s := fmt.Sprintf("key/remote(%q).%s", addr, method)
	op := &operation{errors.Op(s), fmt.Sprintf(format, args...)}
	log.Debug.Print(op)
	return op
}

type operation struct {
	op   errors.Op
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
