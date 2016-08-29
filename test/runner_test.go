// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"upspin.io/client"
	"upspin.io/errors"
	"upspin.io/upspin"
)

type testRunner struct {
	Entry   *upspin.DirEntry
	Entries []*upspin.DirEntry
	Data    string

	user    upspin.UserName
	clients map[upspin.UserName]upspin.Client

	err     error
	errFile string
	errLine int
	lastErr error // used by Diag
}

func newRunner() *testRunner {
	return &testRunner{clients: make(map[upspin.UserName]upspin.Client)}
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
func (r *testRunner) AddUser(ctx upspin.Context) *testRunner {
	if r.err != nil {
		return r
	}
	r.clients[ctx.UserName()] = client.New(ctx)
	return r
}

// As instructs the Runner to perform subsequent actions as the specified user.
// It must have been first added with AddUser.
func (r *testRunner) As(u upspin.UserName) *testRunner {
	if r.err != nil {
		return r
	}
	c := r.clients[u]
	if c == nil {
		r.setErr(errors.E(errors.NotExist, u))
		return r
	}
	r.user = u
	return r
}

// Get performs a Get request as the current user,
// and populates the Runner's Data field with the result.
func (r *testRunner) Get(p upspin.PathName) *testRunner {
	if r.err != nil {
		return r
	}
	data, err := r.clients[r.user].Get(p)
	r.Data = string(data)
	r.setErr(err)
	return r
}

// Put performs a Put request as the current user,
// and populates the Runner's Entry field with the result.
func (r *testRunner) Put(p upspin.PathName, data string) *testRunner {
	if r.err != nil {
		return r
	}
	entry, err := r.clients[r.user].Put(p, []byte(data))
	r.Entry = entry
	r.setErr(err)
	return r
}

// Mkdir performs a MakeDirectory request as the current user,
// and populates the Runner's Entry field with the result.
func (r *testRunner) Mkdir(p upspin.PathName) *testRunner {
	if r.err != nil {
		return r
	}
	entry, err := r.clients[r.user].MakeDirectory(p)
	r.Entry = entry
	r.setErr(err)
	return r
}

// Delete performs a Delete request as the current user.
func (r *testRunner) Delete(p upspin.PathName) *testRunner {
	if r.err != nil {
		return r
	}
	err := r.clients[r.user].Delete(p)
	r.setErr(err)
	return r
}

// Glob performs a Glob request as the current user,
// and populates the Runner's Entries field with the result.
// If there is exactly one entry in Entries
// the Entry field will also be populated with that entry.
func (r *testRunner) Glob(pattern string) *testRunner {
	if r.err != nil {
		return r
	}
	entries, err := r.clients[r.user].Glob(pattern)
	r.Entries = entries
	if len(entries) == 1 {
		r.Entry = entries[0]
	}
	r.setErr(err)
	return r
}

// Err returns the current error state and clears it.
func (r *testRunner) Err() error {
	err := r.err
	r.err = nil
	r.lastErr = err
	return err
}

// Failed reports whether the current error state is non-nil, and clears it.
func (r *testRunner) Failed() bool {
	return r.Err() != nil
}

// Check reports whether the current error state is non-nil,
// and if so it calls t.Fatal with that error information.
func (r *testRunner) Check(t *testing.T) *testRunner {
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	return r
}

// Match checks whether the current error state matches the given error,
// and if not it calls t.Fatal noting the discrepancy.
func (r *testRunner) Match(t *testing.T, want error) *testRunner {
	got := r.Err()
	if errors.Match(want, got) {
		return r
	}
	if got == nil {
		r.err = errors.Errorf("got nil error, want %q", want)
	} else {
		r.err = errors.Errorf("got error %q, want %q", got, want)
	}
	return r.Check(t)
}

// Diag returns a string containing the last checked error
// and the file and line in which the error occurred.
func (r *testRunner) Diag() string {
	if r.lastErr == nil {
		return "<nil>"
	}
	if r.errFile == "" {
		return r.lastErr.Error()
	}
	return fmt.Sprintf("%v:%v: %v", filepath.Base(r.errFile), r.errLine, r.lastErr)
}
