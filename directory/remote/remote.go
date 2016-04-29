// Package testdir implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	"errors"
	"fmt"
	"net/rpc"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/directory/proto"
	"upspin.googlesource.com/upspin.git/upspin"

	// Imported because it's used to pack dir entries.
	_ "upspin.googlesource.com/upspin.git/pack/plain"
)

// remote implements upspin.Directory.
type remote struct {
	endpoint  upspin.Endpoint
	rpcClient *rpc.Client
}

var _ upspin.Directory = (*remote)(nil)

// Glob implements upspin.Directory.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	req := &proto.GlobRequest{
		Pattern: pattern,
	}
	var resp proto.GlobResponse
	err := r.rpcClient.Call("Server.Glob", &req, &resp)
	return resp.Entries, err
}

// MakeDirectory implements upspin.Directory.MakeDirectory.
func (r *remote) MakeDirectory(directoryName upspin.PathName) (upspin.Location, error) {
	req := &proto.MakeDirectoryRequest{
		Name: directoryName,
	}
	var resp proto.MakeDirectoryResponse
	err := r.rpcClient.Call("Server.MakeDirectory", &req, &resp)
	return resp.Location, err
}

// Put implements upspin.Directory.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (r *remote) Put(entry *upspin.DirEntry) error {
	req := &proto.PutRequest{
		Entry: entry,
	}
	var resp proto.PutResponse
	return r.rpcClient.Call("Server.Put", &req, &resp)
}

// WhichAccess implements upspin.Directory.WhichAccess.
func (r *remote) WhichAccess(pathName upspin.PathName) (upspin.PathName, error) {
	req := &proto.WhichAccessRequest{
		Name: pathName,
	}
	var resp proto.WhichAccessResponse
	err := r.rpcClient.Call("Server.WhichAccess", &req, &resp)
	return resp.Name, err
}

// Delete implements upspin.Directory.Delete.
func (r *remote) Delete(pathName upspin.PathName) error {
	req := &proto.DeleteRequest{
		Name: pathName,
	}
	var resp proto.DeleteResponse
	return r.rpcClient.Call("Server.Delete", &req, &resp)
}

// Lookup implements upspin.Directory.Lookup.
func (r *remote) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	req := &proto.LookupRequest{
		Name: pathName,
	}
	var resp proto.LookupResponse
	err := r.rpcClient.Call("Server.Lookup", &req, &resp)
	return resp.Entry, err
}

// Methods to implement upspin.Dialer

// ServerUserName implements upspin.Dialer.
func (r *remote) ServerUserName() string {
	return "" // No one is authenticated.
}

// Dial always returns the same instance, so there is only one instance of the service
// running in the address space. It ignores the address within the endpoint but
// requires that the transport be InProcess.
func (r *remote) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
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

// Endpoint implements upspin.Directory.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.endpoint
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterDirectory(transport, r)
}
