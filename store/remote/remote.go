// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess store server that uses RPC to
// connect to a remote store server.
package remote // import "upspin.io/store/remote"

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"upspin.io/bind"
	"upspin.io/errors"
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

// remote implements upspin.StoreServer.
type remote struct {
	rpc.Client // For sessions and Close.
	cfg        dialConfig

	// probeOnce is used to make sure we call probeDirect just once.
	probeOnce sync.Once
	// If non-empty, the base HTTP URL under which references for this
	// server may be found. It is set while probeOnce is happening, so
	// probeDirect must be called before using baseURL.
	baseURL string
}

var _ upspin.StoreServer = (*remote)(nil)

// Get implements upspin.StoreServer.Get.
func (r *remote) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	op := r.opf("Get", "%q", ref)

	if !strings.HasPrefix(string(ref), "metadata:") {
		if err := r.probeDirect(); err != nil {
			op.error(err)
		}
		if r.baseURL != "" {
			// If we can fetch this by HTTP, do so.
			u := r.baseURL + string(ref)
			resp, err := http.Get(u)
			if err != nil {
				return nil, nil, nil, op.error(err)
			}
			if resp.StatusCode != http.StatusOK {
				err := errors.Errorf("fetching %s: %s", u, resp.Status)
				if resp.StatusCode == http.StatusNotFound {
					err = errors.E(errors.NotExist, err)
				}
				return nil, nil, nil, op.error(err)
			}
			body, err := ioutil.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, nil, nil, op.error(err)
			}
			refData := &upspin.Refdata{
				Reference: ref,
				Volatile:  false,
				Duration:  0,
			}
			return body, refData, nil, nil
		}
	}

	req := &proto.StoreGetRequest{
		Reference: string(ref),
	}
	resp := new(proto.StoreGetResponse)
	if err := r.Invoke("Store/Get", req, resp, nil, nil); err != nil {
		return nil, nil, nil, op.error(err)
	}
	if len(resp.Error) != 0 {
		return nil, nil, nil, errors.UnmarshalError(resp.Error)
	}
	return resp.Data, proto.UpspinRefdata(resp.Refdata), proto.UpspinLocations(resp.Locations), nil
}

// Put implements upspin.StoreServer.Put.
func (r *remote) Put(data []byte) (*upspin.Refdata, error) {
	op := r.opf("Put", "%.16x...) (%v bytes", data, len(data))

	req := &proto.StorePutRequest{
		Data: data,
	}
	resp := new(proto.StorePutResponse)
	if err := r.Invoke("Store/Put", req, resp, nil, nil); err != nil {
		return nil, op.error(err)
	}
	return proto.UpspinRefdata(resp.Refdata), op.error(errors.UnmarshalError(resp.Error))
}

// Delete implements upspin.StoreServer.Delete.
func (r *remote) Delete(ref upspin.Reference) error {
	op := r.opf("Delete", "%q", ref)

	req := &proto.StoreDeleteRequest{
		Reference: string(ref),
	}
	resp := new(proto.StoreDeleteResponse)
	if err := r.Invoke("Store/Delete", req, resp, nil, nil); err != nil {
		return op.error(err)
	}
	return op.error(errors.UnmarshalError(resp.Error))
}

// Endpoint implements upspin.StoreServer.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.cfg.endpoint
}

func dialCache(config upspin.Config, proxyFor upspin.Endpoint) (upspin.Service, error) {
	// Are we using a cache?
	ce := config.CacheEndpoint()
	if ce.Unassigned() {
		return nil, nil
	}

	// Call the cache. The cache is local so don't bother with TLS.
	authClient, err := rpc.NewClient(config, ce.NetAddr, rpc.NoSecurity, proxyFor)
	if err != nil {
		return nil, err
	}

	return &remote{
		Client: authClient,
		cfg: dialConfig{
			endpoint: proxyFor,
			userName: config.UserName(),
		},
	}, nil
}

// Dial implements upspin.Service.
func (r *remote) Dial(config upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	op := r.opf("Dial", "%q, %q", config.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, errors.Str("unrecognized transport"))
	}

	// First try a cache
	if svc, err := dialCache(config, e); err != nil {
		return nil, err
	} else if svc != nil {
		return svc, nil
	}

	// Call the server directly.
	authClient, err := rpc.NewClient(config, e.NetAddr, rpc.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	r2 := &remote{
		Client: authClient,
		cfg: dialConfig{
			endpoint: e,
			userName: config.UserName(),
		},
	}
	return r2, nil
}

// probeDirect performs a Get request to the remote server for the reference
// httpBaseRef. The server may respond with an HTTP URL that may be used as a
// base for fetching objects directly by HTTP (from Google Cloud Storage, for
// instance).
func (r *remote) probeDirect() error {
	var err error
	r.probeOnce.Do(func() {
		b, _, _, err2 := r.Get(upspin.HTTPBaseMetadata)
		if errors.Is(errors.NotExist, err2) {
			return
		}
		if err2 != nil {
			err = err2
			return
		}
		s := string(b)

		u, err2 := url.Parse(s)
		if err2 != nil {
			err = errors.Errorf("parsing %q: %v", s, err2)
			return
		}

		// We have a valid URL. Use it as a base.
		r.baseURL = u.String()
	})
	return err
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterStoreServer(transport, r)
}

func (r *remote) opf(method string, format string, args ...interface{}) *operation {
	addr := r.cfg.endpoint.NetAddr
	s := fmt.Sprintf("store/remote(%q).%s", addr, method)
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
