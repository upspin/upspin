// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dircache

import (
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"

	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

var noError error
var notExistError = errors.E(errors.NotExist)

const testUser = "u@foo.com"

var dirBlock1 = upspin.DirBlock{
	Location: upspin.Location{
		Endpoint:  *ep1,
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
	if le == nil {
		return errors.Errorf("%s not found", l)
	}
	estr := ""
	if l.name != le.name {
		estr += " names"
	}
	if l.request != le.request {
		estr += " request"
	}
	if l.complete != le.complete {
		estr += " complete"
	}
	if !reflect.DeepEqual(l.de, le.de) {
		estr += " de"
	}
	if !reflect.DeepEqual(l.children, le.children) {
		estr += " children"
	}
	if !reflect.DeepEqual(l.error, le.error) {
		estr += " error"
	}
	if estr != "" {
		estr = strings.TrimSpace(estr)
		return errors.Errorf("mismatch <%s> {%s} vs {%s}", estr, l, le)
	}
	return nil
}

func compareReply(l *clogEntry, de *upspin.DirEntry, err error) error {
	le := &clogEntry{request: l.request, name: l.name, complete: l.complete, error: err, de: de}
	return compare(l, le)
}

var (
	ep1 = &upspin.Endpoint{Transport: upspin.InProcess, NetAddr: upspin.NetAddr("hoohaa")}
)

var goodLogEntries = []clogEntry{
	{request: lookupReq, name: upspin.PathName("u@foo.com/a/b/c"), error: noError},
	{request: lookupReq, name: upspin.PathName("u@foo.com/a/b/c"), error: notExistError},
	{request: lookupReq, name: upspin.PathName("u@foo.com/a/b/c"), error: upspin.ErrFollowLink},
	{request: globReq, name: upspin.PathName("u@foo.com/a/b/c"),
		children: map[string]bool{"q": true, "r": true, "s": true}, error: noError, complete: true},
	{request: globReq, name: upspin.PathName("u@foo.com/a/b/c"),
		children: map[string]bool{}, error: noError, complete: true},
}

var badLogEntries = []clogEntry{
	{request: request(maxReq), error: notExistError},
}

func TestMarshal(t *testing.T) {
	for _, good := range goodLogEntries {
		addDirEntry(&good)
		b, err := good.marshal()
		if err != nil {
			t.Errorf("%s: marshal error %v", &good, err)
			continue
		}
		var e clogEntry
		if err := e.unmarshal(b); err != nil {
			t.Errorf("%s: unmarshal failed: %v", &e, err)
			continue
		}
		if err := compare(&good, &e); err != nil {
			t.Error(err)
		}
	}
	for _, bad := range badLogEntries {
		_, err := bad.marshal()
		if err == nil {
			t.Errorf("%s: marshal should have failed", &bad)
		}
	}
}

var names = []string{
	"u@foo.com/a/file",
	"u@foo.com/a/b/file",
	"u@foo.com/a/b/c/file",
}

func TestLogFile(t *testing.T) {
	log.SetLevel("debug")
	dir, err := ioutil.TempDir("", "dircacheserverlog")
	if err != nil {
		t.Fatalf("creating test directory: %v", err)
	}
	defer os.RemoveAll(dir)
	l, err := openLog(config.SetUserName(config.New(), testUser), dir, 1000000)
	if err != nil {
		t.Fatalf("creating test log: %v", err)
	}

	// Ensure that the log LRU contains what we put into it.
	t.Logf("TestLogFile test LRU")
	for _, name := range names {
		good := mkClogEntry(putReq, name)
		l.logRequest(good.request, good.name, good.error, good.de)
	}
	for _, name := range names {
		good := mkClogEntry(putReq, name)
		de, err, ok := l.lookup(good.name)
		if !ok {
			t.Errorf("%s not found", good.name)
		}
		if cerr := compareReply(good, de, err); err != nil {
			t.Error(cerr)
		}
	}
	l.close()

	// Reopen and check the LRU contents.
	t.Logf("TestLogFile test LogFile")
	l, err = openLog(config.SetUserName(config.New(), testUser), dir, 1000000)
	if err != nil {
		t.Fatal("creating test log")
	}
	for _, name := range names {
		good := mkClogEntry(putReq, name)
		de, err, ok := l.lookup(good.name)
		if !ok {
			t.Errorf("%s not found", good.name)
		}
		if cerr := compareReply(good, de, err); err != nil {
			t.Error(cerr)
		}
	}

	// Append something that will be compress the log.
	for i, name := range names {
		if i == 0 {
			continue
		}
		good := clogEntry{request: deleteReq, name: upspin.PathName(name)}
		l.logRequest(good.request, good.name, good.error, nil)
	}
	for i, name := range names {
		good := mkClogEntry(putReq, name)
		_, _, ok := l.lookup(good.name)
		if i == 0 {
			if !ok {
				t.Errorf("%s: expected but not found", good)
			}
		} else {
			if ok {
				t.Errorf("%s: not expected but found", good)
			}
		}
	}
	l.close()

	// Reopen and make sure it is still compressed.
	l, err = openLog(config.SetUserName(config.New(), testUser), dir, 1000000)
	if err != nil {
		t.Fatal("creating test log")
	}
	for i, name := range names {
		good := mkClogEntry(putReq, name)
		_, _, ok := l.lookup(good.name)
		if i == 0 {
			if !ok {
				t.Errorf("%s: expected but not found", good)
			}
		} else {
			if ok {
				t.Errorf("%s: not expected but found", good)
			}
		}
	}
	l.close()
}

// TestLogGlob ensures that the glob saves all the included DirEntries and then a Glob record.
func TestLogGlob(t *testing.T) {
	dir, err := ioutil.TempDir("", "dircacheserverlog")
	if err != nil {
		t.Fatal("creating test directory")
	}
	defer os.RemoveAll(dir)
	l, err := openLog(config.SetUserName(config.New(), testUser), dir, 1000000)
	if err != nil {
		t.Fatal("creating test log")
	}

	// Log the glob entry.
	var entries []*upspin.DirEntry
	for i := 0; i < 10; i++ {
		de := mkDirEntry(fmt.Sprintf("u@foo.com/a/b/c/%d", i))
		de.Sequence = int64(upspin.SeqBase + i)
		entries = append(entries, de)
	}
	l.logGlobRequest("u@foo.com/a/b/c/*", nil, entries)

	// Check for individual entries.
	nentries, err, ok := l.lookupGlob("u@foo.com/a/b/c/*")
	if err != nil {
		t.Fatalf("lookupGlob: %v", err)
	}
	if !ok {
		t.Fatalf("lookupGlob not found")
	}
	if len(nentries) != len(entries) {
		t.Fatalf("lookupGlob missing entries: %d instead of %d", len(nentries), len(entries))
	}
l:
	for _, ode := range entries {
		for _, nde := range nentries {
			if reflect.DeepEqual(nde, ode) {
				continue l
			}
		}
		t.Fatalf("lookupGlob missing %v", *ode)
	}
	l.close()
	t.Log("reopening log")

	// Reopen, and ensure the glob services.
	l, err = openLog(config.SetUserName(config.New(), testUser), dir, 1000000)
	if err != nil {
		t.Fatal("creating test log")
	}
	nentries, err, ok = l.lookupGlob("u@foo.com/a/b/c/*")
	if err != nil {
		t.Fatalf("lookupGlob: %v", err)
	}
	if !ok {
		t.Fatalf("lookupGlob not found")
	}
	if len(nentries) != len(entries) {
		t.Fatalf("lookupGlob (after reopen) missing entries: %d instead of %d", len(nentries), len(entries))
	}
l2:
	for _, ode := range entries {
		for _, nde := range nentries {
			if reflect.DeepEqual(nde, ode) {
				continue l2
			}
		}
		t.Fatalf("lookupGlob (after reopen) missing %v", *ode)
	}
	l.close()
}

func mkClogEntry(r request, name string) *clogEntry {
	e := &clogEntry{
		request: r,
		name:    upspin.PathName(name),
	}
	addDirEntry(e)
	return e
}

func addDirEntry(e *clogEntry) {
	if e.error != nil && e.error != upspin.ErrFollowLink {
		return
	}
	e.de = mkDirEntry(string(e.name))
}

func mkDirEntry(name string) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name:       upspin.PathName(name),
		SignedName: upspin.PathName(name),
		Packing:    upspin.EEPack,
		Time:       123456,
		Blocks: []upspin.DirBlock{
			dirBlock1,
			dirBlock2,
		},
		Link:     "",
		Packdata: []byte{1, 2, 3, 4},
		Attr:     upspin.AttrNone,
		Sequence: 1234,
		Writer:   testUser,
	}
}
