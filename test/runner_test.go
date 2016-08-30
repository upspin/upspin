// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"path/filepath"
	"runtime"

	"upspin.io/client"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// testRunner (name to be changed) is a helper for writing tests that interact
// with Upspin trees. It can perform actions as multiple users in tandem. It
// reduces error handling boilerplate by tracking error state and skipping all
// actions between where an error occurs and where it is checked.
//
// 	r := newRunner()
// 	r.AddUser(context)
// 	r.As(username)
// 	r.Put("user@host/foo", "content")
// 	r.Get("user@host/foo")
// 	if r.Failed() {
// 		t.Fatal(r.Diag())
// 	}
type testRunner struct {
	// Entry holds the result of the most recent Put or MakeDirectory operation.
	Entry *upspin.DirEntry

	// Entries holds the result of the most recent Glob operation.
	Entries []*upspin.DirEntry

	// Data holds the result of the most recent Get operation.
	Data string

	user    upspin.UserName
	clients map[upspin.UserName]upspin.Client

	err     error
	errFile string
	errLine int
	lastErr error // used by Diag
}

func newRunner() *testRunner {
	return &testRunner{
		clients: make(map[upspin.UserName]upspin.Client),
	}
}

func (r *testRunner) setErr(err error) {
	if r.err != nil {
		return
	}
	r.err = err
	_, r.errFile, r.errLine, _ = runtime.Caller(2)
}

// AddUser adds the user in the given context to the Runner's
// internal state, and creates a client for use as that user.
// If a client already exists for that user, it is replaced with a new one.
func (r *testRunner) AddUser(ctx upspin.Context) {
	if r.err != nil {
		return
	}
	r.clients[ctx.UserName()] = client.New(ctx)
}

// As instructs the Runner to perform subsequent actions as the specified user.
// It must have been first added with AddUser.
func (r *testRunner) As(u upspin.UserName) {
	if r.err != nil {
		return
	}
	c := r.clients[u]
	if c == nil {
		r.setErr(errors.E(errors.NotExist, u))
		return
	}
	r.user = u
}

// Get performs a Get request as the user
// and populates the Runner's Data field with the result.
func (r *testRunner) Get(p upspin.PathName) {
	if r.err != nil {
		return
	}
	data, err := r.clients[r.user].Get(p)
	r.Data = string(data)
	r.setErr(err)
}

// Put performs a Put request as the user
// and populates the Runner's Entry field with the result.
func (r *testRunner) Put(p upspin.PathName, data string) {
	if r.err != nil {
		return
	}
	entry, err := r.clients[r.user].Put(p, []byte(data))
	r.Entry = entry
	r.setErr(err)
}

// PutLink performs a PutLink request as the user
// and populates the Runner's Entry field with the result.
func (r *testRunner) PutLink(oldName, linkName upspin.PathName) {
	if r.err != nil {
		return
	}
	entry, err := r.clients[r.user].PutLink(oldName, linkName)
	r.Entry = entry
	r.setErr(err)
}

// MakeDirectory performs a MakeDirectory request as the user
// and populates the Runner's Entry field with the result.
func (r *testRunner) MakeDirectory(p upspin.PathName) {
	if r.err != nil {
		return
	}
	entry, err := r.clients[r.user].MakeDirectory(p)
	r.Entry = entry
	r.setErr(err)
}

// Delete performs a Delete request as the user.
func (r *testRunner) Delete(p upspin.PathName) {
	if r.err != nil {
		return
	}
	err := r.clients[r.user].Delete(p)
	r.setErr(err)
}

// Glob performs a Glob request as the user,
// and populates the Runner's Entries field with the result.
func (r *testRunner) Glob(pattern string) {
	if r.err != nil {
		return
	}
	entries, err := r.clients[r.user].Glob(pattern)
	r.Entries = entries
	r.setErr(err)
}

// DirLookup performs a Lookup request to the user's underlying DirServer
// and populates the Runner's Entry field with the result.
func (r *testRunner) DirLookup(p upspin.PathName) {
	if r.err != nil {
		return
	}
	dir, err := r.clients[r.user].DirServer(p)
	if err != nil {
		r.setErr(err)
		return
	}
	entry, err := dir.Lookup(p)
	r.Entry = entry
	r.setErr(err)
}

// Err returns the current error state and clears it.
func (r *testRunner) Err() error {
	err := r.err
	r.err = nil
	r.lastErr = err
	return err
}

// Failed reports whether the current error state is non-nil,
// saves the error for use by the Diag method,
// and clears the error state.
func (r *testRunner) Failed() bool {
	return r.Err() != nil
}

// Match checks whether the current error state matches the given error,
// and if not it notes the discrepancy as the last error state,
// otherwise it clears the error.
func (r *testRunner) Match(want error) bool {
	got := r.Err()
	if want == got || errors.Match(want, got) {
		return true
	}
	if got == nil {
		r.lastErr = errors.Errorf("got nil error, want %q", want)
	} else {
		r.lastErr = errors.Errorf("got error:\n\t%v\nwant:\n\t%v", got, want)
	}
	return false
}

// Diag returns a string containing the most recent saved error
// and the file and line at which the error occurred.
func (r *testRunner) Diag() string {
	if r.lastErr == nil {
		return "<nil>"
	}
	if r.errFile == "" {
		return r.lastErr.Error()
	}
	return fmt.Sprintf("%s:%d: %v", filepath.Base(r.errFile), r.errLine, r.lastErr)
}
