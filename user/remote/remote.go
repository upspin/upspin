// Package remote implements an inprocess user server that uses RPC to
// connect to a remote user server.
package remote

import (
	"errors"
	"fmt"
	"net/rpc"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/proto"
)

// remote implements upspin.User.
type remote struct {
	upspin.NoConfiguration // TODO: Change this so configuration can initialize users?
	endpoint               upspin.Endpoint
	rpcClient              *rpc.Client
}

var _ upspin.User = (*remote)(nil)

// Lookup implements upspin.User.Lookup.
func (r *remote) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	req := &proto.LookupRequest{
		UserName: name,
	}
	var resp proto.LookupResponse
	err := r.rpcClient.Call("Server.Lookup", &req, &resp)
	if len(resp.Endpoints) == 0 {
		resp.Endpoints = nil
	}
	if len(resp.PublicKeys) == 0 {
		resp.PublicKeys = nil
	}
	return resp.Endpoints, resp.PublicKeys, err
}

// ServerUserName implements upspin.Service.
func (r *remote) ServerUserName() string {
	return "" // No one is authenticated.
}

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (r *remote) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.New("remote: unrecognized transport")
	}

	var err error
	addr := string(e.NetAddr)
	switch {
	case strings.HasPrefix(addr, "http://"):
		r.rpcClient, err = rpc.DialHTTP("tcp", addr[7:])
	default:
		err = fmt.Errorf("unrecognized net address in remote: %q", addr)
	}
	if err != nil {
		return nil, err
	}
	r.endpoint = e
	return r, nil
}

// Endpoint implements upspin.User.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.endpoint
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterUser(transport, r)
}
