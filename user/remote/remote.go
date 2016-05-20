// Package remote implements an inprocess user server that uses RPC to
// connect to a remote user server.
package remote

import (
	"errors"
	"fmt"
	"net/rpc"
	"strings"
	"sync"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/proto"
)

// dialContext contains the destination and authenticated user of the dial.
type dialContext struct {
	endpoint upspin.Endpoint
	userName upspin.UserName
}

// remote implements upspin.User.
type remote struct {
	ctx       dialContext
	rpcClient *rpc.Client
}

// remotes contains a list of all established remote connections.
var remotes struct {
	sync.Mutex
	r map[dialContext]*remote
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

// Dial implements upspin.Service.
func (*remote) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.New("remote: unrecognized transport")
	}

	r := &remote{
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName,
		},
	}

	// If we already have an authenticated dial for the endpoint and user
	// return it.
	remotes.Lock()
	if nr, ok := remotes.r[r.ctx]; ok {
		remotes.Unlock()
		return nr, nil
	}
	remotes.Unlock()

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

	remotes.Lock()
	remotes.r[r.ctx] = r
	remotes.Unlock()
	return r, nil
}

// Endpoint implements upspin.User.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

// Configure implements upspin.Service.
func (r *remote) Configure(options ...string) error {
	req := &proto.ConfigureRequest{
		Options: options,
	}
	var resp proto.ConfigureResponse
	return r.rpcClient.Call("Server.Configure", &req, &resp)
}

func (r *remote) Ping() bool {
	// TODO: possibly not the best way to find the server. WILL NOT work when we remove the "http://" prefix.
	return netutil.IsServerReachable(string(r.ctx.endpoint.NetAddr))
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterUser(transport, r)
	remotes.r = make(map[dialContext]*remote)
}
