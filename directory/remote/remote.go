// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	"errors"
	"fmt"
	"net/rpc"
	"strings"
	"time"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/directory/proto"
	"upspin.googlesource.com/upspin.git/upspin"
)

// dialContext contains the destination and authenticated user of the dial.
type dialContext struct {
	endpoint upspin.Endpoint
	userName upspin.UserName
	factotum upspin.Factotum
}

// remote implements upspin.Directory.
type remote struct {
	upspin.NoConfiguration
	ctx       dialContext
	id        int
	rpcClient *rpc.Client
}

var _ upspin.Directory = (*remote)(nil)

// call calls the RPC method for the user associated with the remote.
func (r *remote) call(method string, req, resp interface{}) error {
	return r.rpcClient.Call(fmt.Sprintf("Server_%d.%s", r.id, method), req, resp)
}

// Glob implements upspin.Directory.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	req := &proto.GlobRequest{
		Pattern: pattern,
	}
	var resp proto.GlobResponse
	err := r.call("Glob", &req, &resp)
	return resp.Entries, err
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (r *remote) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	req := &proto.MakeDirectoryRequest{
		Name: directoryName,
	}
	var resp proto.MakeDirectoryResponse
	err := r.call("MakeDirectory", &req, &resp)
	return resp.Location, err
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (r *remote) Put(entry *upspin.DirEntry) error {
	req := &proto.PutRequest{
		Entry: entry,
	}
	var resp proto.PutResponse
	return r.call("Put", &req, &resp)
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (r *remote) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	req := &proto.WhichAccessRequest{
		Name: pathName,
	}
	var resp proto.WhichAccessResponse
	err := r.call("WhichAccess", &req, &resp)
	return resp.Name, err
}

// Delete implements upspin.Directory.Delete.
func (r *remote) Delete(pathName upspin.PathName) error {
	req := &proto.DeleteRequest{
		Name: pathName,
	}
	var resp proto.DeleteResponse
	return r.call("Delete", &req, &resp)
}

// Lookup implements upspin.Directory.Lookup.
func (r *remote) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	req := &proto.LookupRequest{
		Name: pathName,
	}
	var resp proto.LookupResponse
	err := r.call("Lookup", &req, &resp)
	return resp.Entry, err
}

// Authenticate tells the server which user this is.
func (r *remote) Authenticate(userName upspin.UserName) (int, error) {
	req := &proto.AuthenticateRequest{
		UserName: userName,
		Now:      time.Now().UTC().Format(time.ANSIC), // to discourage signature replay
	}
	sig, err := r.ctx.factotum.UserSign([]byte(string(req.UserName) + " DirectoryAuthenticate " + req.Now))
	if err != nil {
		return -1, err
	}
	req.Signature = sig
	var resp proto.AuthenticateResponse
	err = r.rpcClient.Call("Server.Authenticate", &req, &resp)
	return resp.ID, err
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
			factotum: context.Factotum,
		},
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
	r.id, err = r.Authenticate(context.UserName)
	if err != nil {
		return nil, err
	}

	return r, nil
}

// Endpoint implements upspin.Directory.Endpoint.
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
	bind.RegisterDirectory(transport, r)
}
