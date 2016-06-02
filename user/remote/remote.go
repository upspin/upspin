// Package remote implements an inprocess user server that uses RPC to
// connect to a remote user server.
package remote

import (
	"errors"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
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

// Dial implements upspin.Service.
func (*remote) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.New("remote user: unrecognized transport")
	}

	const allowSelfSignedCertificate = true // for documenting the parameter
	conn, err := grpcauth.NewGRPCClient(e.NetAddr, allowSelfSignedCertificate)
	if err != nil {
		return nil, err
	}
	// TODO: When can we do conn.Close()?
	userClient := proto.NewUserClient(conn)
	if err != nil {
		return nil, err
	}
	authClient := grpcauth.AuthClientService{
		GRPCCommon: userClient,
		GRPCConn:   conn,
		Context:    context,
	}
	r := &remote{
		AuthClientService: authClient,
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
