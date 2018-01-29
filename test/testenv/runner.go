// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testenv

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// Runner is a helper for writing tests that interact with Upspin trees.
// It can perform actions as multiple users in tandem. It reduces error
// handling boilerplate by tracking error state and skipping all actions
// between where an error occurs and where it is checked.
//
// 	r := testenv.NewRunner()
// 	r.AddUser(config)
// 	r.As(username)
// 	r.Put("user@host/foo", "content")
// 	r.Get("user@host/foo")
// 	if r.Failed() {
// 		t.Fatal(r.Diag())
// 	}
type Runner struct {
	// Entry holds the result of the most recent Put, DirLookup or
	// MakeDirectory operation.
	Entry *upspin.DirEntry

	// Entries holds the result of the most recent Glob operation.
	Entries []*upspin.DirEntry

	// Data holds the result of the most recent Get operation.
	Data string

	// Events holds the result of the most recent GetEvents operation.
	Events []upspin.Event

	user    upspin.UserName
	configs map[upspin.UserName]upspin.Config
	clients map[upspin.UserName]upspin.Client
	events  map[upspin.UserName]<-chan upspin.Event

	err     error
	errFile string
	errLine int
	lastErr error // used by Diag
}

func NewRunner() *Runner {
	return &Runner{
		configs: make(map[upspin.UserName]upspin.Config),
		clients: make(map[upspin.UserName]upspin.Client),
		events:  make(map[upspin.UserName]<-chan upspin.Event),
	}
}

func (r *Runner) setErr(err error) {
	if r.err != nil {
		return
	}
	r.err = err
	_, r.errFile, r.errLine, _ = runtime.Caller(2)
}

// AddUser adds the user in the given config to the Runner's
// internal state, and creates a client for use as that user.
// If a client already exists for that user, it is replaced with a new one.
func (r *Runner) AddUser(cfg upspin.Config) {
	if r.err != nil {
		return
	}
	r.configs[cfg.UserName()] = cfg
	r.clients[cfg.UserName()] = client.New(cfg)
}

// As instructs the Runner to perform subsequent actions as the specified user.
// It must have been first added with AddUser.
func (r *Runner) As(u upspin.UserName) {
	if r.err != nil {
		return
	}
	c := r.clients[u]
	if c == nil {
		r.setErr(errors.E(errors.NotExist, u))
		return
	}
	r.setErr(r.FlushCache())
	r.user = u
}

// Config returns the Config for the current user.
func (r *Runner) Config() upspin.Config {
	cfg := r.configs[r.user]
	if cfg == nil {
		return config.New()
	}
	return cfg
}

// Get performs a Get request as the user
// and populates the Runner's Data field with the result.
func (r *Runner) Get(p upspin.PathName) {
	if r.err != nil {
		return
	}
	data, err := r.clients[r.user].Get(p)
	r.Data = string(data)
	r.setErr(err)
}

// Put performs a Put request as the user
// and populates the Runner's Entry field with the result.
func (r *Runner) Put(p upspin.PathName, data string) {
	if r.err != nil {
		return
	}
	entry, err := r.clients[r.user].Put(p, []byte(data))
	r.Entry = entry
	r.setErr(err)
}

// PutLink performs a PutLink request as the user
// and populates the Runner's Entry field with the result.
func (r *Runner) PutLink(oldName, linkName upspin.PathName) {
	if r.err != nil {
		return
	}
	entry, err := r.clients[r.user].PutLink(oldName, linkName)
	r.Entry = entry
	r.setErr(err)
}

// MakeDirectory creates a directory by issuing a Put request
// as the user and populates the Runner's Entry field with the result.
func (r *Runner) MakeDirectory(p upspin.PathName) {
	if r.err != nil {
		return
	}
	entry, err := r.clients[r.user].MakeDirectory(p)
	r.Entry = entry
	r.setErr(err)
}

// Delete performs a Delete request as the user.
func (r *Runner) Delete(p upspin.PathName) {
	if r.err != nil {
		return
	}
	err := r.clients[r.user].Delete(p)
	r.setErr(err)
}

// Glob performs a Glob request as the user
// and populates the Runner's Entries field with the result.
func (r *Runner) Glob(pattern string) {
	if r.err != nil {
		return
	}
	entries, err := r.clients[r.user].Glob(pattern)
	r.Entries = entries
	r.setErr(err)
}

// DirWhichAccess performs a WhichAccess request to the user's underlying
// DirServer and populates the Runner's Entry field with the result.
func (r *Runner) DirWhichAccess(p upspin.PathName) {
	if r.err != nil {
		return
	}
	dir, err := r.clients[r.user].DirServer(p)
	if err != nil {
		r.setErr(err)
		return
	}
	entry, err := dir.WhichAccess(p)
	r.Entry = entry
	r.setErr(err)
}

// DirLookup performs a Lookup request to the user's underlying DirServer
// and populates the Runner's Entry field with the result.
func (r *Runner) DirLookup(p upspin.PathName) {
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

// DirWatch performs a Watch request to the user's underlying DirServer and
// populates the Runner's Events channel with the DirServer's returned Event
// channel. It returns the done channel for this watcher, if successful.
func (r *Runner) DirWatch(p upspin.PathName, seq int64) chan struct{} {
	if r.err != nil {
		return nil
	}
	dir, err := r.clients[r.user].DirServer(p)
	if err != nil {
		r.setErr(err)
		return nil
	}
	done := make(chan struct{})
	r.events[r.user], err = dir.Watch(p, seq, done)
	r.setErr(err)
	return done
}

// GotEntry reports whether the Entry has the given name
// and if not notes the discrepancy as the last error state.
func (r *Runner) GotEntry(p upspin.PathName) bool {
	if r.Failed() {
		return false
	}
	if r.Entry != nil && r.Entry.Name == p {
		return true
	}
	if r.Entry == nil {
		r.lastErr = errors.Errorf("got nil entry, want %q", p)
	} else {
		r.lastErr = errors.Errorf("got entry %q, want %q", r.Entry.Name, p)
	}
	_, r.errFile, r.errLine, _ = runtime.Caller(1)
	return false
}

// GotIncompleteEntry reports whether the Entry has attribute Incomplete and
// does not have populated Blocks and Packdata fields.
// If not, it notes the discrepancy as the last error state.
func (r *Runner) GotIncompleteEntry(p upspin.PathName) bool {
	if !r.GotEntry(p) {
		return false
	}
	if !r.Entry.IsIncomplete() {
		r.lastErr = errors.Str("does not have AttrIncomplete, should be set")
	} else if r.Entry.Blocks != nil {
		r.lastErr = errors.Str("has non-nil Blocks, want nil")
	} else if r.Entry.Packdata != nil {
		r.lastErr = errors.Str("has non-nil Packdata, want nil")
	} else {
		return true
	}
	_, r.errFile, r.errLine, _ = runtime.Caller(2)
	return false
}

// GotNilEntry reports whether the Entry is nil and if not notes this fact as
// the last error state.
func (r *Runner) GotNilEntry() bool {
	if r.Failed() {
		return false
	}
	if r.Entry == nil {
		return true
	}
	r.lastErr = errors.Errorf("got entry %q, want nil", r.Entry.Name)
	_, r.errFile, r.errLine, _ = runtime.Caller(1)
	return false
}

// GotEntryWithSequence reports whether the Entry has the given name
// and sequence number and if not notes the discrepancy as the last error state.
func (r *Runner) GotEntryWithSequence(p upspin.PathName, seq int64) bool {
	if r.Failed() {
		return false
	}
	if r.Entry == nil {
		r.lastErr = errors.Errorf("got nil entry, want %q", p)
	} else if r.Entry.Name != p {
		r.lastErr = errors.Errorf("got entry %q, want %q", r.Entry.Name, p)
	} else if r.Entry.Sequence != seq {
		r.lastErr = errors.Errorf("got sequence %d, want %d", r.Entry.Sequence, seq)
	} else {
		return true
	}
	_, r.errFile, r.errLine, _ = runtime.Caller(1)
	return false
}

// GotEntryWithSequenceVersion reports whether the Entry has the given name
// and sequence version number and if not notes the discrepancy as the last error state.
func (r *Runner) GotEntryWithSequenceVersion(p upspin.PathName, seq int64) bool {
	if r.Failed() {
		return false
	}
	if r.Entry == nil {
		r.lastErr = errors.Errorf("got nil entry, want %q", p)
	} else if r.Entry.Name != p {
		r.lastErr = errors.Errorf("got entry %q, want %q", r.Entry.Name, p)
	} else if r.Entry.Sequence != seq {
		r.lastErr = errors.Errorf("got sequence %d, want %d", r.Entry.Sequence, seq)
	} else {
		return true
	}
	_, r.errFile, r.errLine, _ = runtime.Caller(1)
	return false
}

// GotEntries reports whether the names of the Entries match the provided
// list (in order). It also checks that the presence of block data in
// those entries matches the boolean, except it tolerates Access and Group files
// having blocks even if wantBlockData is false.
// If not, it notes the discrepancy as the last error state.
func (r *Runner) GotEntries(wantBlockData bool, ps ...upspin.PathName) bool {
	if r.Failed() {
		return false
	}
	if got, want := len(r.Entries), len(ps); got != want {
		var names string
		if len(r.Entries) > 0 {
			var ns []string
			for _, e := range r.Entries {
				ns = append(ns, string(e.Name))
			}
			names = "; got entries:\n\t" + strings.Join(ns, "\n\t")
		}
		r.lastErr = errors.Errorf("got %d entries, want %d%s", got, want, names)
		_, r.errFile, r.errLine, _ = runtime.Caller(1)
		return false
	}
	for i, want := range ps {
		got := r.Entries[i].Name
		if got != want {
			r.lastErr = errors.Errorf("got entry %d:\n\t%q\nwant:\n\t%q", i, got, want)
			_, r.errFile, r.errLine, _ = runtime.Caller(1)
			return false
		}
		nBlocks := len(r.Entries[i].Blocks)
		if nBlocks > 0 == wantBlockData || r.Entries[i].IsLink() {
			continue
		}
		if nBlocks > 0 && !wantBlockData && access.IsAccessControlFile(r.Entries[i].Name) {
			// Access and Group file can have blocks in case the reader has any right.
			continue
		}
		if wantBlockData {
			r.lastErr = errors.Errorf("got entry %q with %d blocks, want some", got, nBlocks)
		} else {
			r.lastErr = errors.Errorf("got entry %q with %d blocks, want none", got, nBlocks)
		}
		_, r.errFile, r.errLine, _ = runtime.Caller(1)
		return false
	}
	return true
}

// Err returns the error state and clears it.
func (r *Runner) Err() error {
	err := r.err
	r.err = nil
	r.lastErr = err
	return err
}

// Failed reports whether the error state is non-nil,
// saves the error for use by the Diag method,
// and clears the error state.
func (r *Runner) Failed() bool {
	return r.Err() != nil
}

// Match checks whether the error state matches the given error
// and if not it notes the discrepancy as the last error state;
// otherwise it clears the error.
func (r *Runner) Match(want error) bool {
	return r.match(want, r.Err())
}

func (r *Runner) match(want, got error) bool {
	if want == got || errorMatch(want, got) {
		return true
	}
	if got == nil {
		r.lastErr = errors.Errorf("got nil error, want %q", want)
	} else {
		r.lastErr = errors.Errorf("got error:\n\t%v\nwant:\n\t%v", got, want)
	}
	return false
}

// GetNEvents receives n events from the event channel of the calling user.
// If it cannot fulfill the request it sets the last error state.
func (r *Runner) GetNEvents(n int) bool {
	if r.Failed() {
		return false
	}
	r.Events = make([]upspin.Event, 0, n)
	for i := 0; i < n; i++ {
		e := r.getNextEvent()
		if e == nil {
			return false
		}
		r.Events = append(r.Events, *e)
	}
	return true
}

// GotEvent reports whether some Event we received has the given
// name and presence of blocks. It notes any discrepancy as the last error state.
func (r *Runner) GotEvent(p upspin.PathName, withBlocks bool) bool {
	if r.Failed() {
		return false
	}
	var got []upspin.PathName
	for _, e := range r.Events {
		got = append(got, e.Entry.Name)
		if e.Entry.Name != p {
			continue
		}
		hasBlocks := len(e.Entry.Blocks) > 0
		if withBlocks {
			if hasBlocks {
				return true
			}
			r.lastErr = errors.Errorf("expected blocks present for %q", p)
			_, r.errFile, r.errLine, _ = runtime.Caller(1)
			return false
		} else if hasBlocks {
			r.lastErr = errors.Errorf("expected no blocks present for %q, got %d", p, len(e.Entry.Blocks))
			_, r.errFile, r.errLine, _ = runtime.Caller(1)
			return false
		}
		return true
	}
	r.lastErr = errors.Errorf("expected Event for %q; got events for these paths instead: %v", p, got)
	_, r.errFile, r.errLine, _ = runtime.Caller(1)
	return false
}

// GetErrorEvent gets one event from the user's Event channel and expects
// it to contain an error. If not, it records the discrepancy in the last error state.
func (r *Runner) GetErrorEvent(want error) bool {
	if r.Failed() {
		return false
	}
	r.getNextEvent()
	return r.match(want, r.lastErr)
}

// GetDeleteEvent gets one event from the user's Event channel and expects
// it to be a deletion of the file.
func (r *Runner) GetDeleteEvent(p upspin.PathName) bool {
	if r.Failed() {
		return false
	}
	event := r.getNextEvent()
	if r.Failed() {
		return false
	}
	if event.Entry.Name != p {
		r.lastErr = errors.E(errors.Errorf("path was %q; expected %q", event.Entry.Name, p))
		return false
	}
	if !event.Delete {
		r.lastErr = errors.E(errors.Errorf("event for %q was not Delete", event.Entry.Name))
		return false
	}
	return true
}

func (r *Runner) getNextEvent() *upspin.Event {
	var e upspin.Event
	var ok bool
	select {
	case e, ok = <-r.events[r.user]:
	case <-time.After(time.Second):
		r.lastErr = errors.E("no response on event channel after one second")
		_, r.errFile, r.errLine, _ = runtime.Caller(2)
		return nil
	}
	if !ok {
		r.lastErr = errors.E("event channel closed")
		_, r.errFile, r.errLine, _ = runtime.Caller(2)
		return nil
	}
	if e.Error != nil {
		r.lastErr = e.Error
		_, r.errFile, r.errLine, _ = runtime.Caller(2)
		return nil
	}
	return &e
}

// Diag returns a string containing the most recent saved error
// and the file and line at which the error occurred.
func (r *Runner) Diag() string {
	if r.lastErr == nil {
		return "<nil>"
	}
	if r.errFile == "" {
		return r.lastErr.Error()
	}
	return fmt.Sprintf("%s:%d: %v", filepath.Base(r.errFile), r.errLine, r.lastErr)
}

// FlushCache flushes a user's Store cache.
func (r *Runner) FlushCache() error {
	ce := r.Config().CacheEndpoint()
	if ce.Unassigned() {
		return nil
	}
	store, err := bind.StoreServer(r.Config(), r.Config().StoreEndpoint())
	if err != nil {
		return err
	}
	if _, _, _, err := store.Get(upspin.FlushWritebacksMetadata); err != nil {
		return err
	}
	return nil
}
