// Package remote implements an inprocess user server that uses RPC to
// connect to a remote user server.
package remote

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
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

// remote implements upspin.User.
type remote struct {
	ctx        dialContext
	userClient proto.UserClient
}

var _ upspin.User = (*remote)(nil)

// Lookup implements upspin.User.Lookup.
func (r *remote) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	req := &proto.UserLookupRequest{
		UserName: string(name),
	}
	resp, err := r.userClient.Lookup(gContext.Background(), req)
	if err != nil {
		return nil, nil, err
	}
	if len(resp.Endpoints) == 0 {
		resp.Endpoints = nil
	}
	if len(resp.PublicKeys) == 0 {
		resp.PublicKeys = nil
	}
	return proto.UpspinEndpoints(resp.Endpoints), proto.UpspinPublicKeys(resp.PublicKeys), err
}

// ServerUserName implements upspin.Service.
func (r *remote) ServerUserName() string {
	return "" // No one is authenticated.
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
	_, err := r.userClient.Configure(gContext.Background(), req)
	return err
}

// Ping implements upspin.Service.
func (r *remote) Ping() bool {
	seq := rand.Int31()
	req := &proto.PingRequest{
		PingSequence: seq,
	}
	resp, err := r.userClient.Ping(gContext.Background(), req)
	return err == nil && resp.PingSequence == seq
}

// Dial implements upspin.Service.
func (*remote) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.New("remote user: unrecognized transport")
	}

	r := &remote{
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName,
		},
	}

	var err error
	addr := string(e.NetAddr)
	switch {
	case strings.HasPrefix(addr, "http://"): // TODO: Should this be, say "grpc:"?
		conn, err := grpcauth.NewGRPCClient(e.NetAddr[7:], requireAuthentication)
		if err != nil {
			return nil, err
		}
		// TODO: When can we do conn.Close()?
		r.userClient = proto.NewUserClient(conn)
	default:
		err = fmt.Errorf("unrecognized net address in user remote: %q", addr)
	}
	if err != nil {
		return nil, err
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterUser(transport, r)
}
