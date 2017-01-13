// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	"fmt"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/transport/auth"
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

// remote implements upspin.DirServer.
type remote struct {
	auth.Client // For sessions, Ping, and Close.
	ctx         dialContext
}

var _ upspin.DirServer = (*remote)(nil)

// Glob implements upspin.DirServer.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	op := r.opf("Glob", "%q", pattern)

	req := &proto.DirGlobRequest{
		Pattern: pattern,
	}
	resp := new(proto.EntriesError)
	if err := r.Invoke("Dir/Glob", req, resp); err != nil {
		return nil, op.error(errors.IO, err)
	}
	err := unmarshalError(resp.Error)
	if err != nil && err != upspin.ErrFollowLink {
		return nil, op.error(err)
	}
	entries, pErr := proto.UpspinDirEntries(resp.Entries)
	if pErr != nil {
		return nil, op.error(errors.IO, pErr)
	}
	return entries, op.error(err)
}

func entryName(entry *upspin.DirEntry) string {
	if entry == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", entry.Name)
}

// Put implements upspin.DirServer.Put.
func (r *remote) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	op := r.opf("Put", "%s", entryName(entry))

	b, err := entry.Marshal()
	if err != nil {
		return nil, op.error(err)
	}
	return r.invoke(op, "Dir/Put", &proto.DirPutRequest{
		Entry: b,
	})
}

// WhichAccess implements upspin.DirServer.WhichAccess.
func (r *remote) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	op := r.opf("WhichAccess", "%q", pathName)

	return r.invoke(op, "Dir/WhichAccess", &proto.DirWhichAccessRequest{
		Name: string(pathName),
	})
}

// Delete implements upspin.DirServer.Delete.
func (r *remote) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	op := r.opf("Delete", "%q", pathName)

	return r.invoke(op, "Dir/Delete", &proto.DirDeleteRequest{
		Name: string(pathName),
	})
}

// Lookup implements upspin.DirServer.Lookup.
func (r *remote) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	op := r.opf("Lookup", "%q", pathName)

	return r.invoke(op, "Dir/Lookup", &proto.DirLookupRequest{
		Name: string(pathName),
	})
}

func (r *remote) invoke(op *operation, method string, req pb.Message) (*upspin.DirEntry, error) {
	resp := new(proto.EntryError)
	err := r.Invoke(method, req, resp)
	return op.entryError(resp, err)
}

// Watch implements upspin.DirServer.
func (r *remote) Watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	return nil, upspin.ErrNotSupported
}

// Endpoint implements upspin.StoreServer.Endpoint.
func (r *remote) Endpoint() upspin.Endpoint {
	return r.ctx.endpoint
}

func dialCache(op *operation, context upspin.Context, proxyFor upspin.Endpoint) upspin.Service {
	// Are we using a cache?
	ce := context.CacheEndpoint()
	if ce.Transport == upspin.Unassigned {
		return nil
	}

	// Call the cache. The cache is local so don't bother with TLS.
	authClient, err := auth.NewClient(context, ce.NetAddr, auth.NoSecurity, proxyFor)
	if err != nil {
		// On error dial direct.
		op.error(errors.IO, err)
		return nil
	}

	return &remote{
		Client: authClient,
		ctx: dialContext{
			endpoint: proxyFor,
			userName: context.UserName(),
		},
	}
}

// Dial implements upspin.Service.
func (r *remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	op := r.opf("Dial", "%q, %q", context.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, errors.Str("unrecognized transport"))
	}

	// First try a cache
	if svc := dialCache(op, context, e); svc != nil {
		return svc, nil
	}

	authClient, err := auth.NewClient(context, e.NetAddr, auth.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	// The connection is closed when this service is released (see Bind.Release)
	r = &remote{
		Client: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName(),
		},
	}

	return r, nil
}

const transport = upspin.Remote

func init() {
	r := &remote{} // uninitialized until Dial time.
	bind.RegisterDirServer(transport, r)
}

// unmarshalError calls proto.UnmarshalError, but if the error
// matches upspin.ErrFollowLink, it returns that exact value so
// the caller can do == to test for links.
func unmarshalError(b []byte) error {
	err := errors.UnmarshalError(b)
	if err != nil && err.Error() == upspin.ErrFollowLink.Error() {
		return upspin.ErrFollowLink
	}
	return err
}

func (r *remote) opf(method string, format string, args ...interface{}) *operation {
	ep := r.ctx.endpoint.String()
	s := fmt.Sprintf("dir/remote.%s(%q)", method, ep)
	op := &operation{s, fmt.Sprintf(format, args...)}
	log.Debug.Print(op)
	return op
}

type operation struct {
	op   string
	args string
}

func (op *operation) String() string {
	return fmt.Sprintf("%s(%s)", op.op, op.args)
}

func (op *operation) logErr(err error) {
	log.Error.Printf("%s: %s", op, err)
}

func (op *operation) logf(format string, args ...interface{}) {
	log.Printf("%s: "+format, append([]interface{}{op.op}, args...)...)
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

// entryError performs the common operation of converting an EntryError
// protocol buffer into a directory entry and error pair.
func (op *operation) entryError(p *proto.EntryError, err error) (*upspin.DirEntry, error) {
	if err != nil {
		return nil, op.error(errors.IO, err)
	}
	err = unmarshalError(p.Error)
	if err != nil && err != upspin.ErrFollowLink {
		return nil, op.error(err)
	}
	entry, unmarshalErr := proto.UpspinDirEntry(p.Entry)
	if unmarshalErr != nil {
		return nil, op.error(unmarshalErr)
	}
	return entry, op.error(err)
}
