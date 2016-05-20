// Package remote implements an inprocess store server that uses RPC to
// connect to a remote store server.
package remote

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	gContext "golang.org/x/net/context"

	"google.golang.org/grpc"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/upspin/proto"
)

// dialContext contains the destination and authenticated user of the dial.
type dialContext struct {
	endpoint upspin.Endpoint
	userName upspin.UserName
}

// remote implements upspin.Store.
type remote struct {
	ctx         dialContext
	storeClient proto.StoreClient
}

// remotes contains a list of all established remote connections.
var remotes struct {
	sync.Mutex
	r map[dialContext]*remote
}

var _ upspin.Store = (*remote)(nil)

// Get implements upspin.Store.Get.
func (r *remote) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	req := &proto.GetRequest{
		Reference: string(ref),
	}
	resp, err := r.storeClient.Get(gContext.Background(), req)
	return resp.Data, proto.UpspinLocations(resp.Locations), err
}

// Put implements upspin.Store.Put.
// Directories are created with MakeDirectory.
func (r *remote) Put(data []byte) (upspin.Reference, error) {
	req := &proto.PutRequest{
		Data: data,
	}
	resp, err := r.storeClient.Put(gContext.Background(), req)
	return upspin.Reference(resp.Reference), err
}

// Delete implements upspin.Store.Delete.
func (r *remote) Delete(ref upspin.Reference) error {
	req := &proto.DeleteRequest{
		Reference: string(ref),
	}
	_, err := r.storeClient.Delete(gContext.Background(), req)
	return err
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
	case strings.HasPrefix(addr, "http://"): // TODO: Should this be, say "grpc:"?
		conn, err := grpc.Dial(addr[7:], grpc.WithInsecure()) // TODO: Enable TLS.
		if err != nil {
			log.Fatalf("remote store: gprc did not connect: %v", err)
		}
		// TODO: When can we do conn.Close()?
		r.storeClient = proto.NewStoreClient(conn)
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

// Endpoint implements upspin.Store.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

// Configure implements upspin.Service.
func (r *remote) Configure(options ...string) error {
	req := &proto.ConfigureRequest{
		Options: options,
	}
	_, err := r.storeClient.Configure(gContext.Background(), req)
	return err
}

// Ping implements uspin.Service.
func (r *remote) Ping() bool {
	// TODO: possibly not the best way to find the server. WILL NOT work when we remove the "http://" prefix.
	return netutil.IsServerReachable(string(r.ctx.endpoint.NetAddr))
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterStore(transport, r)
	remotes.r = make(map[dialContext]*remote)
}
