// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dircacheserver is a caching proxy between a client and all directories.
// Cached entries are appended to a log to survive restarts.
package dircacheserver

import (
	"flag"
	"fmt"
	"os"
	ospath "path"
	"runtime"
	"runtime/pprof"
	"sync"

	gContext "golang.org/x/net/context"

	"upspin.io/auth/grpcauth"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// server is a SecureServer that talks to a DirServer interface and serves GRPC requests.
type server struct {
	ctx  upspin.Context
	clog *clog

	// epMap is a map of users to endpoints
	epMap *epMap

	// Automatically handles authentication by implementing the Authenticate server method.
	grpcauth.SecureServer
}

// New creates a new DirServer cache reading in the log and writing out a new compacted log.
func New(ctx upspin.Context, ss grpcauth.SecureServer) (proto.DirServer, error) {
	homeDir := os.Getenv("HOME")
	if len(homeDir) == 0 {
		return nil, errors.Str("$HOME not defined")
	}
	epMap := newEpMap()
	clog, err := openLog(ctx, ospath.Join(homeDir, "upspin/dircache"), 20*1024*1024, epMap)
	if err != nil {
		return nil, err
	}
	return &server{
		ctx:          ctx,
		clog:         clog,
		epMap:        epMap,
		SecureServer: ss,
	}, nil
}

// dirFor returns a DirServer instance bound to the user specified in the context.
func (s *server) dirFor(ctx gContext.Context, path upspin.PathName) (upspin.DirServer, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ep := session.ProxiedEndpoint()
	if ep.Transport == upspin.Unassigned {
		return nil, errors.Str("not yet configured")
	}
	dir, err := bind.DirServer(s.ctx, ep)
	if err == nil {
		s.epMap.Set(path, &ep)
	}
	return dir, err
}

// endpointFor returns a DirServer endpoint for the context.
func (s *server) endpointFor(ctx gContext.Context) (*upspin.Endpoint, error) {
	var ep upspin.Endpoint
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return &ep, err
	}
	ep = session.ProxiedEndpoint()
	if ep.Transport == upspin.Unassigned {
		return &ep, errors.Str("not yet configured")
	}
	return &ep, nil
}

// Lookup implements proto.DirServer.
func (s *server) Lookup(ctx gContext.Context, req *proto.DirLookupRequest) (*proto.EntryError, error) {
	op := logf("Lookup %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	lock := s.clog.lock(name)
	defer s.clog.unlock(lock)
	if e := s.clog.lookup(name); e != nil {
		return entryError(e.de, e.error)
	}

	de, err := dir.Lookup(name)
	s.clog.logRequest(lookupReq, name, err, de)

	return entryError(de, err)
}

// Glob implements proto.DirServer.
func (s *server) Glob(ctx gContext.Context, req *proto.DirGlobRequest) (*proto.EntriesError, error) {
	op := logf("Glob %q", req.Pattern)

	name := path.Clean(upspin.PathName(req.Pattern))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entriesError(nil, err)
	}

	lock := s.clog.lock(name)
	defer s.clog.unlock(lock)
	if e, entries := s.clog.lookupGlob(name); e != nil {
		return entriesError(entries, e.error)
	}

	entries, globReqErr := dir.Glob(string(name))
	s.clog.logGlobRequest(name, globReqErr, entries)

	return entriesError(entries, globReqErr)
}

// Put implements proto.DirServer.
// TODO(p): Remember access errors to avoid even trying?
func (s *server) Put(ctx gContext.Context, req *proto.DirPutRequest) (*proto.EntryError, error) {
	entry, err := proto.UpspinDirEntry(req.Entry)
	entry.Name = path.Clean(entry.Name)
	if err != nil {
		return &proto.EntryError{Error: errors.MarshalError(err)}, nil
	}
	op := logf("Put %q", entry.Name)

	dir, err := s.dirFor(ctx, entry.Name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	lock := s.clog.lock(entry.Name)
	defer s.clog.unlock(lock)
	de, err := dir.Put(entry)
	s.clog.logRequest(putReq, entry.Name, err, de)

	return entryError(de, err)
}

// Delete implements proto.DirServer.
func (s *server) Delete(ctx gContext.Context, req *proto.DirDeleteRequest) (*proto.EntryError, error) {
	op := logf("Delete %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	lock := s.clog.lock(name)
	defer s.clog.unlock(lock)
	de, err := dir.Delete(name)
	s.clog.logRequest(deleteReq, name, err, de)

	return entryError(de, err)
}

// WhichAccess implements proto.DirServer.
func (s *server) WhichAccess(ctx gContext.Context, req *proto.DirWhichAccessRequest) (*proto.EntryError, error) {
	op := logf("WhichAccess %q", req.Name)

	name := path.Clean(upspin.PathName(req.Name))
	dir, err := s.dirFor(ctx, name)
	if err != nil {
		op.log(err)
		return entryError(nil, err)
	}

	lock := s.clog.lock(name)
	defer s.clog.unlock(lock)
	if de, ok := s.clog.whichAccess(name); ok {
		return entryError(de, nil)
	}
	de, err := dir.WhichAccess(name)
	s.clog.logRequest(whichAccessReq, name, err, de)

	return entryError(de, err)
}

// Watch implements proto.Watch.
func (s *server) Watch(stream proto.Dir_WatchServer) error {
	return stream.Send(&proto.Event{
		Error: errors.MarshalError(upspin.ErrNotSupported),
	})
}

// Endpoint implements proto.DirServer.
func (s *server) Endpoint(ctx gContext.Context, req *proto.EndpointRequest) (*proto.EndpointResponse, error) {

	op := logf("Endpoint")

	ep, err := s.endpointFor(ctx)
	if err != nil {
		op.log(err)
		return &proto.EndpointResponse{}, err
	}
	return &proto.EndpointResponse{
		Endpoint: &proto.Endpoint{
			Transport: int32(ep.Transport),
			NetAddr:   string(ep.NetAddr),
		},
	}, nil
}

func logf(format string, args ...interface{}) operation {
	s := fmt.Sprintf(format, args...)
	log.Debug.Print("grpc/dircacheserver: " + s)
	return operation(s)
}

type operation string

func (op operation) log(err error) {
	logf("%v failed: %v", op, err)
}

// entryError performs the common operation of converting a directory entry
// and error result pair into the corresponding protocol buffer.
func entryError(entry *upspin.DirEntry, err error) (*proto.EntryError, error) {
	var b []byte
	if entry != nil {
		var mErr error
		b, mErr = entry.Marshal()
		if mErr != nil {
			return nil, mErr
		}
	}
	return &proto.EntryError{
		Entry: b,
		Error: errors.MarshalError(err),
	}, nil
}

// entriesError performs the common operation of converting a list of directory entries
// and error result pair into the corresponding protocol buffer.
func entriesError(entries []*upspin.DirEntry, err error) (*proto.EntriesError, error) {
	if err != nil && err != upspin.ErrFollowLink {
		return globError(err), nil
	}
	// Fall through OK for ErrFollowLink.

	b, mErr := proto.DirEntryBytes(entries)
	if mErr != nil {
		return globError(mErr), nil
	}
	return &proto.EntriesError{
		Entries: b,
		Error:   errors.MarshalError(err),
	}, nil
}

func globError(err error) *proto.EntriesError {
	return &proto.EntriesError{Error: errors.MarshalError(err)}
}

// epMap is a cache from user name to the endpoing of its directory server.
type epMap struct {
	sync.Mutex
	m map[upspin.UserName]*upspin.Endpoint
}

func newEpMap() *epMap {
	return &epMap{m: make(map[upspin.UserName]*upspin.Endpoint)}
}

func (c *epMap) Set(p upspin.PathName, ep *upspin.Endpoint) {
	c.Lock()
	if parsed, err := path.Parse(p); err != nil {
		c.m[parsed.User()] = ep
	}
	c.Unlock()
}

func (c *epMap) Get(p upspin.PathName) *upspin.Endpoint {
	c.Lock()
	defer c.Unlock()
	if parsed, err := path.Parse(p); err == nil {
		return nil
	} else {
		return c.m[parsed.User()]
	}
}

var memprofile = flag.String("memprofile", "", "write memory profile to `file`")

func dumpMemStats() {
	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatalf("could not create memory profile: %s", err)
		}
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatalf("could not write memory profile: %s", err)
		}
		f.Close()
	}
	dump("")
}

var oldm runtime.MemStats

func dump(tag string) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Info.Printf("%s Alloc:\t%d\t%d\n", tag, int64(m.Alloc), int64(m.Alloc)-int64(oldm.Alloc))
	log.Info.Printf("%s TotalAlloc:\t%d\t%d\n", tag, int64(m.TotalAlloc), int64(m.TotalAlloc)-int64(oldm.TotalAlloc))
	log.Info.Printf("%s Sys:\t%d\t%d\n", tag, int64(m.Sys), int64(m.Sys)-int64(oldm.Sys))
	log.Info.Printf("%s HeapAlloc:\t%d\t%d\n", tag, int64(m.HeapAlloc), int64(m.HeapAlloc)-int64(oldm.HeapAlloc))
	log.Info.Printf("%s HeapSys:\t%d\t%d\n", tag, int64(m.HeapSys), int64(m.HeapSys)-int64(oldm.HeapSys))
	log.Info.Printf("%s StackInuse:\t%d\t%d\n", tag, int64(m.StackInuse), int64(m.StackInuse)-int64(oldm.StackInuse))
	log.Info.Printf("%s StackSys:\t%d\t%d\n", tag, int64(m.StackSys), int64(m.StackSys)-int64(oldm.StackSys))
	log.Info.Printf("%s MSpanInuse:\t%d\t%d\n", tag, int64(m.MSpanInuse), int64(m.MSpanInuse)-int64(oldm.MSpanInuse))
	log.Info.Printf("%s MSpanSys:\t%d\t%d\n", tag, int64(m.MSpanSys), int64(m.MSpanSys)-int64(oldm.MSpanSys))
	log.Info.Printf("%s MCacheInuse:\t%d\t%d\n", tag, int64(m.MCacheInuse), int64(m.MCacheInuse)-int64(oldm.MCacheInuse))
	log.Info.Printf("%s MCacheSys:\t%d\t%d\n", tag, int64(m.MCacheSys), int64(m.MCacheSys)-int64(oldm.MCacheSys))
	log.Info.Printf("%s BuckHashSys:\t%d\t%d\n", tag, int64(m.BuckHashSys), int64(m.BuckHashSys)-int64(oldm.BuckHashSys))
	log.Info.Printf("%s OtherSys:\t%d\t%d\n", tag, int64(m.OtherSys), int64(m.OtherSys)-int64(oldm.OtherSys))
	log.Info.Printf("\n")
	oldm = m
}
