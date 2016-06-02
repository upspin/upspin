// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	"errors"

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

// remote implements upspin.Directory.
type remote struct {
	upspin.NoConfiguration
	grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	ctx                        dialContext
	dirClient                  proto.DirectoryClient
}

var _ upspin.Directory = (*remote)(nil)

// Glob implements upspin.Directory.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	gCtx, err := r.SetAuthContext(r.Context)
	if err != nil {
		return nil, err
	}
	req := &proto.DirectoryGlobRequest{
		Pattern: pattern,
	}
	resp, err := r.dirClient.Glob(gCtx, req)
	if err != nil {
		return nil, err
	}
	if len(resp.Entries) == 0 {
		return nil, err
	}
	return proto.UpspinDirEntries(resp.Entries)
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (r *remote) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	gCtx, err := r.SetAuthContext(r.Context)
	if err != nil {
		return upspin.Location{}, err
	}
	req := &proto.DirectoryMakeDirectoryRequest{
		Name: string(directoryName),
	}
	resp, err := r.dirClient.MakeDirectory(gCtx, req)
	if err != nil {
		return upspin.Location{}, err
	}
	return proto.UpspinLocation(resp.Location), err
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (r *remote) Put(entry *upspin.DirEntry) error {
	gCtx, err := r.SetAuthContext(r.Context)
	if err != nil {
		return err
	}
	b, err := entry.Marshal()
	if err != nil {
		return err
	}
	req := &proto.DirectoryPutRequest{
		Entry: b,
	}
	_, err = r.dirClient.Put(gCtx, req)
	return err
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (r *remote) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	gCtx, err := r.SetAuthContext(r.Context)
	if err != nil {
		return "", err
	}
	req := &proto.DirectoryWhichAccessRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.WhichAccess(gCtx, req)
	if err != nil {
		return "", err
	}
	return upspin.PathName(resp.Name), err
}

// Delete implements upspin.Directory.Delete.
func (r *remote) Delete(pathName upspin.PathName) error {
	gCtx, err := r.SetAuthContext(r.Context)
	if err != nil {
		return err
	}
	req := &proto.DirectoryDeleteRequest{
		Name: string(pathName),
	}
	_, err = r.dirClient.Delete(gCtx, req)
	return err
}

// Lookup implements upspin.Directory.Lookup.
func (r *remote) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	gCtx, err := r.SetAuthContext(r.Context)
	if err != nil {
		return nil, err
	}
	req := &proto.DirectoryLookupRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.Lookup(gCtx, req)
	if err != nil {
		return nil, err
	}
	return proto.UpspinDirEntry(resp.Entry)
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
	_, err := r.dirClient.Configure(gContext.Background(), req)
	return err
}

// Dial implements upspin.Service.
func (*remote) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.Remote {
		return nil, errors.New("remote: unrecognized transport")
	}

	conn, err := grpcauth.NewGRPCClient(e.NetAddr, grpcauth.AllowSelfSignedCertificate)
	if err != nil {
		return nil, err
	}
	// TODO: When can we do conn.Close()?
	dirClient := proto.NewDirectoryClient(conn)
	authClient := grpcauth.AuthClientService{
		GRPCCommon: dirClient,
		GRPCConn:   conn,
		Context:    context,
	}
	r := &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName,
		},
		dirClient: dirClient,
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterDirectory(transport, r)
}
