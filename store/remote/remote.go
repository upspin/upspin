// Package remote implements an inprocess store server that uses RPC to
// connect to a remote store server.
package remote

import (
	"errors"
	"fmt"
	"net/rpc"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/store/proto"
	"upspin.googlesource.com/upspin.git/upspin"
)

// remote implements upspin.Store.
type remote struct {
	upspin.NoConfiguration
	endpoint  upspin.Endpoint
	rpcClient *rpc.Client
}

var _ upspin.Store = (*remote)(nil)

// Get implements upspin.Store.Get.
func (r *remote) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	req := &proto.GetRequest{
		Reference: ref,
	}
	var resp proto.GetResponse
	err := r.rpcClient.Call("Server.Get", &req, &resp)
	if len(resp.Locations) == 0 {
		resp.Locations = nil
	}
	return resp.Data, resp.Locations, err
}

// Put implements upspin.Store.Put.
// Directories are created with MakeDirectory. Roots are anyway. TODO?.
func (r *remote) Put(data []byte) (upspin.Reference, error) {
	req := &proto.PutRequest{
		Data: data,
	}
	var resp proto.PutResponse
	err := r.rpcClient.Call("Server.Put", &req, &resp)
	return resp.Reference, err
}

// Delete implements upspin.Store.Delete.
func (r *remote) Delete(ref upspin.Reference) error {
	req := &proto.DeleteRequest{
		Reference: ref,
	}
	var resp proto.DeleteResponse
	return r.rpcClient.Call("Server.Delete", &req, &resp)
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

// Endpoint implements upspin.Store.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.endpoint
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterStore(transport, r)
}
