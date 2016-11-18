// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package remote implements an inprocess directory server that uses RPC to
// connect to a remote directory server.
package remote

import (
	"fmt"

	"io"
	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
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
	*grpcauth.AuthClientService // For handling Authenticate, Ping and Close.
	ctx                         dialContext
	dirClient                   proto.DirClient
}

var _ upspin.DirServer = (*remote)(nil)

// Glob implements upspin.DirServer.Glob.
func (r *remote) Glob(pattern string) ([]*upspin.DirEntry, error) {
	op := opf("Glob", "%q", pattern)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.DirGlobRequest{
		Pattern: pattern,
	}
	resp, err := r.dirClient.Glob(gCtx, req, callOpt)
	err = finishAuth(err)
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	err = unmarshalError(resp.Error)
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
	op := opf("Put", "%s", entryName(entry))

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	b, err := entry.Marshal()
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.DirPutRequest{
		Entry: b,
	}
	resp, err := r.dirClient.Put(gCtx, req, callOpt)
	err = finishAuth(err)
	return op.entryError(resp, err)
}

// WhichAccess implements upspin.DirServer.WhichAccess.
func (r *remote) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	op := opf("WhichAccess", "%q", pathName)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.DirWhichAccessRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.WhichAccess(gCtx, req, callOpt)
	err = finishAuth(err)
	return op.entryError(resp, err)
}

// Delete implements upspin.DirServer.Delete.
func (r *remote) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	op := opf("Delete", "%q", pathName)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.DirDeleteRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.Delete(gCtx, req, callOpt)
	err = finishAuth(err)
	return op.entryError(resp, err)
}

// Lookup implements upspin.DirServer.Lookup.
func (r *remote) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	op := opf("Lookup", "%q", pathName)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.DirLookupRequest{
		Name: string(pathName),
	}
	resp, err := r.dirClient.Lookup(gCtx, req, callOpt)
	err = finishAuth(err)
	return op.entryError(resp, err)
}

// Watch implements upspin.DirServer.
func (r *remote) Watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	op := opf("Watch", "%q %d", name, order)

	gCtx, callOpt, finishAuth, err := r.NewAuthContext()
	if err != nil {
		return nil, op.error(err)
	}
	srvStream, err := r.dirClient.Watch(gCtx, callOpt)
	err = finishAuth(err)
	if err != nil {
		return nil, op.error(err)
	}
	req := &proto.DirWatchRequest{
		Name:  string(name),
		Order: order,
	}
	err = srvStream.Send(req)
	if err != nil {
		return nil, op.error(err)
	}

	// First event back determines if Watch call was successful.
	protoEvent, err := srvStream.Recv()
	if err != nil {
		return nil, op.error(err)
	}
	event, err := proto.UpspinEvent(protoEvent)
	if err != nil {
		return nil, op.error(err)
	}
	if event.Error != nil {
		return nil, event.Error
	}
	// Assert event.Entry is nil, since this is a synthetic event, which
	// serves to confirm whether Watch succeeded or not.
	if event.Entry != nil {
		return nil, op.error(errors.Internal, errors.Str("first event must have nil entry"))
	}
	// No error on Watch. Establish output channel.
	events := make(chan upspin.Event, 1)
	serverDone := make(chan struct{})
	go r.watchDone(op, srvStream, events, done, serverDone)
	go r.watch(op, srvStream, events, serverDone)
	return events, nil
}

// watchDone, which runs in a goroutine, waits for either the client to close
// its done channel or the server to close its Events channel. When either one
// happens it closes the send-side GRPC stream, which in turn signals the server
// to finish up on its side.
func (r *remote) watchDone(op operation, srvStream proto.Dir_WatchClient, events chan upspin.Event, done, serverDone <-chan struct{}) {
	select {
	case <-done:
		op.logf("Client closed done channel")
	case <-serverDone:
		op.logf("Server closed events channel")
	}
	srvStream.CloseSend()
}

// watch, which runs in a goroutine, receives events from the server and relays
// them to the client onto the events channel. When the server terminates the
// stread (sends io.EOF), we close events channel and notify the other goroutine
// that we've terminated.
//
// Note 1: If the client is done (signalled by closing its
// done channel), the client will close the GRPC stream and the server will
// close its end, which will make this goroutine terminate naturally.
// Note 2: there's no need for sending of events to time out since this is the
// client side.
func (r *remote) watch(op operation, srvStream proto.Dir_WatchClient, events chan upspin.Event, serverDone <-chan struct{}) {
	defer close(events)
	defer close(serverDone) // tell other goroutine the server is done.

	for {
		protoEvent, err := srvStream.Recv()
		if err == io.EOF {
			// Server closed the channel. Nothing else to do but to
			// close ours too.
			return
		}
		if err != nil {
			op.logErr(err)
			events <- upspin.Event{Error: err}
			return
		}
		event, err := proto.UpspinEvent(protoEvent)
		if err != nil {
			op.logErr(err)
			events <- upspin.Event{Error: err}
			return
		}
		events <- event
	}
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
	authClient, err := grpcauth.NewGRPCClient(context, ce.NetAddr, grpcauth.KeepAliveInterval, grpcauth.NoSecurity, proxyFor)
	if err != nil {
		// On error dial direct.
		op.error(errors.IO, ce, err)
		return nil
	}

	// The connection is closed when this service is released (see Bind.Release).
	dirClient := proto.NewDirClient(authClient.GRPCConn())
	authClient.SetService(dirClient)

	return &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: proxyFor,
			userName: context.UserName(),
		},
		dirClient: dirClient,
	}
}

// Dial implements upspin.Service.
func (*remote) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	op := opf("Dial", "%q, %q", context.UserName(), e)

	if e.Transport != upspin.Remote {
		return nil, op.error(errors.Invalid, errors.Str("unrecognized transport"))
	}

	// First try a cache
	r := dialCache(op, context, e)
	if r != nil {
		return r, nil
	}

	authClient, err := grpcauth.NewGRPCClient(context, e.NetAddr, grpcauth.KeepAliveInterval, grpcauth.Secure, upspin.Endpoint{})
	if err != nil {
		return nil, op.error(errors.IO, err)
	}

	// The connection is closed when this service is released (see Bind.Release)
	dirClient := proto.NewDirClient(authClient.GRPCConn())
	authClient.SetService(dirClient)
	r = &remote{
		AuthClientService: authClient,
		ctx: dialContext{
			endpoint: e,
			userName: context.UserName(),
		},
		dirClient: dirClient,
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

func opf(method string, format string, args ...interface{}) *operation {
	op := &operation{"dir/remote." + method, fmt.Sprintf(format, args...)}
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

func (op operation) logErr(err error) {
	log.Error("%s: %s", op, err)
}

func (op operation) logf(args ...interface{}) {
	log.Printf(args...)
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
