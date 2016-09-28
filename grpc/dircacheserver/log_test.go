// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircacheserver

import (
	"io/ioutil"
	"os"
	"reflect"
	"testing"
	"time"

	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/upspin"
)

var noError error
var notExistError = errors.E(errors.NotExist, errors.Str("does not exist"))
var errFollowLink = errors.E(upspin.ErrFollowLink)

var dirEnt = upspin.DirEntry{
	Name:       "u@foo.com/a/directory",
	SignedName: "u@foo.com/a/directory",
	Packing:    upspin.EEPack,
	Time:       123456,
	Blocks: []upspin.DirBlock{
		dirBlock1,
		dirBlock2,
	},
	Link:     "",
	Packdata: []byte{1, 2, 3, 4},
	Attr:     upspin.AttrDirectory, // Just so it's not zero; this is not a semantically valid entry.
	Sequence: 1234,
	Writer:   "u@foo.com",
}

var dirBlock1 = upspin.DirBlock{
	Location: upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   "foo.com:1234",
		},
		Reference: "Cinder",
	},
	Offset:   0,
	Size:     1024,
	Packdata: []byte("sign"),
}

var dirBlock2 = upspin.DirBlock{
	Location: upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.InProcess,
			NetAddr:   "foo.com:1234",
		},
		Reference: "Ice",
	},
	Offset:   1024,
	Size:     4096,
	Packdata: []byte("nature"),
}

// compare compares two log enries for approximate equality.
func compare(l, le *clogEntry) error {
	if le.request != l.request {
		return errors.Errorf("%v: ops don't match: want %v got %v", l.request, le.request)
	}
	if le.ep.Transport != l.ep.Transport || le.ep.NetAddr != l.ep.NetAddr {
		return errors.Errorf("%v: endpoints don't match: want %v got %v", l.ep, le.ep)
	}
	if le.name != l.name {
		return errors.Errorf("%v: names don't match: want %v got %v", l.name, le.name)
	}
	if !errors.Match(l.error, le.error) {
		if l.error != nil || le.error != nil {
			return errors.Errorf("%v: errors don't match: want %v got %v", *l, *le)
		}
	}
	if len(l.entries) != len(le.entries) {
		return errors.Errorf("%v: number of Direntries don't match, %v", l, le)
	}
	for i := range l.entries {
		if !reflect.DeepEqual(*l.entries[i], *le.entries[i]) {
			return errors.Errorf("%v: want DirEntry %v, got DirEntry %v", l.entries[i], le.entries[i])
		}
	}
	return nil
}

var ep1 = &upspin.Endpoint{Transport: upspin.InProcess, NetAddr: upspin.NetAddr("hoohaa")}
var ep2 = &upspin.Endpoint{Transport: upspin.Remote, NetAddr: upspin.NetAddr("upspin.io:124")}

var goodLogEntries = []clogEntry{
	{request: lookupReq, ep: ep1, name: upspin.PathName("x@y.com/a/b/c"), error: noError, entries: []*upspin.DirEntry{&dirEnt}},
	{request: lookupReq, ep: ep1, name: upspin.PathName("x@y.com/a/b/c"), error: notExistError},
	{request: lookupReq, ep: ep2, name: upspin.PathName("x@y.com/a/b/c"), error: errFollowLink, entries: []*upspin.DirEntry{&dirEnt}},
	{request: globReq, ep: ep2, name: upspin.PathName("x@y.com/a/b/c"), error: noError, entries: []*upspin.DirEntry{&dirEnt, &dirEnt, &dirEnt}},
}

var badLogEntries = []clogEntry{
	{request: request(maxReq), error: notExistError},
}

func TestMarshal(t *testing.T) {
	for _, good := range goodLogEntries {
		b, err := good.marshal()
		if err != nil {
			t.Errorf("%v: marshal error %v", good, err)
			continue
		}
		var e clogEntry
		if err := e.unmarshal(b); err != nil {
			t.Errorf("%v: unmarshal failed: %v", e, err)
			continue
		}
		if err := compare(&good, &e); err != nil {
			t.Errorf("%v: %v", good, err)
		}
	}
	for _, bad := range badLogEntries {
		_, err := bad.marshal()
		if err == nil {
			t.Errorf("%v: marshal should have failed", bad)
		}
	}
}

var names = []string{
	"a/file",
	"a/b/file",
	"a/b/c/file",
}

func TestLogFile(t *testing.T) {
	dir, err := ioutil.TempDir("/tmp", "dircacheserverlog")
	if err != nil {
		t.Fatal("creating test directory")
	}
	defer os.RemoveAll(dir)
	l, err := openLog(context.New(), dir, time.Hour)
	if err != nil {
		t.Fatal("creating test log")
	}

	// Ensure that the log LRU contains what we putReq into it.
	for _, name := range names {
		good := clogEntry{request: putReq, ep: ep2, name: upspin.PathName(name), entries: []*upspin.DirEntry{&dirEnt}}
		l.logRequest(good.request, good.ep, good.name, good.error, good.entries[0])
	}
	for _, name := range names {
		good := clogEntry{request: putReq, ep: ep2, name: upspin.PathName(name), entries: []*upspin.DirEntry{&dirEnt}}
		e := l.lookup(good.ep, good.name)
		if err := compare(&good, e); err != nil {
			t.Errorf("%v: %v", good, err)
		}
	}
	l.close()

	// Reopen and check the LRU contents.
	l, err = openLog(context.New(), dir, time.Hour)
	if err != nil {
		t.Fatal("creating test log")
	}
	for _, name := range names {
		good := clogEntry{request: putReq, ep: ep2, name: upspin.PathName(name), entries: []*upspin.DirEntry{&dirEnt}}
		e := l.lookup(good.ep, good.name)
		if err := compare(&good, e); err != nil {
			t.Errorf("%v: %v", good, err)
		}
	}

	// Append something that will be compress the log.
	for i, name := range names {
		if i == 0 {
			continue
		}
		good := clogEntry{request: deleteReq, ep: ep2, name: upspin.PathName(name)}
		l.logRequest(good.request, good.ep, good.name, good.error, nil)
	}
	for i, name := range names {
		good := clogEntry{request: putReq, ep: ep2, name: upspin.PathName(name), entries: []*upspin.DirEntry{&dirEnt}}
		e := l.lookup(good.ep, good.name)
		if i == 0 {
			if e == nil {
				t.Errorf("%v: expected but not found", good, err)
			}
		} else {
			if e != nil {
				t.Errorf("%v: not expected but found", good, err)
			}
		}
	}
	l.close()

	// Reopen and make sure it is still compressed.
	l, err = openLog(context.New(), dir, time.Hour)
	if err != nil {
		t.Fatal("creating test log")
	}
	for i, name := range names {
		good := clogEntry{request: putReq, ep: ep2, name: upspin.PathName(name), entries: []*upspin.DirEntry{&dirEnt}}
		e := l.lookup(good.ep, good.name)
		if i == 0 {
			if e == nil {
				t.Errorf("%v: expected but not found", good, err)
			}
		} else {
			if e != nil {
				t.Errorf("%v: not expected but found", good, err)
			}
		}
	}
	l.close()
}
