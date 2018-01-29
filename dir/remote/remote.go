// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote // import "upspin.io/dir/remote"

import (
	"fmt"

	pb "github.com/golang/protobuf/proto"

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

// remote implements upspin.DirServer.
type remote struct {
	rpc.Client // For sessions and Close.
	cfg        dialConfig
}

var _ upspin.DirServer = (*remote)(nil)

// Glob implements upspin.DirServer.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	op := r.opf("Glob", "%q", pattern)

	req := &proto.DirGlobRequest{
		Pattern: pattern,
	}
	resp := new(proto.EntriesError)
	if err := r.Invoke("Dir/Glob", req, resp, nil, nil); err != nil {
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
	err := r.Invoke(method, req, resp, nil, nil)
	return op.entryError(resp, err)
}

// Watch implements upspin.DirServer.
func (r *remote) Watch(name upspin.PathName, sequence int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	op := r.opf("Watch", "%q sequence %d", name, sequence)
	req := &proto.DirWatchRequest{
		Name:     string(name),
		Sequence: sequence,
	}

	stream := make(eventStream)
	events := make(chan upspin.Event)
	go func() {
		defer close(events)
		for {
			select {
			case ep, ok := <-stream:
				if !ok {
					return
				}
				e, err := proto.UpspinEvent(&ep)
				if err != nil {
					op.logErr(err)
					return
				}
				select {
				case events <- *e:
				case <-done:
					return
				}

			case <-done:
				return
			}
		}
	}()

	if err := r.Invoke("Dir/Watch", req, nil, stream, done); err != nil {
		close(stream)
		if err == upspin.ErrNotSupported {
			return nil, err
		}
		return nil, op.error(err)
	}
	return events, nil
}

type eventStream chan proto.Event

func (s eventStream) Send(b []byte, done <-chan struct{}) error {
	var e proto.Event
	if err := pb.Unmarshal(b, &e); err != nil {
		return err
	}
	select {
	case s <- e:
	case <-done:
	}
	return nil
}

func (s eventStream) Close() { close(s) }

func (s eventStream) Error(err error) {
	s <- proto.Event{Error: errors.MarshalError(err)}
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
		return nil, op.error(errors.Invalid, "unrecognized transport")
	}

	// First try a cache
	if svc, err := dialCache(config, e); err != nil {
		return nil, op.error(err)
	} else if svc != nil {
		return svc, nil
	}

	authClient, err := rpc.NewClient(config, e.NetAddr, rpc.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	// The connection is closed when this service is released (see Bind.Release)
	r = &remote{
		Client: authClient,
		cfg: dialConfig{
			endpoint: e,
			userName: config.UserName(),
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
	addr := r.cfg.endpoint.NetAddr
	s := fmt.Sprintf("dir/remote(%q).%s", addr, method)
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

func (op *operation) logErr(err error) {
	log.Error.Printf("%s: %s", op, err)
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
