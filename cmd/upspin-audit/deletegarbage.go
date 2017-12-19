// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"os"
	"strings"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

func (s *State) deleteGarbage(args []string) {
	const help = `
Audit delete-garbage deletes garbage blocks as listed by the most recent
run of find-garbage. It operates on the store endpoint of the current user.

It must be run as the same Upspin user as the store server itself,
as only that user has permission to delete blocks.

Misuse of this command may result in permanent data loss. Use with caution.
`
	fs := flag.NewFlagSet("delete-garbage", flag.ExitOnError)
	dataDir := dataDirFlag(fs)
	s.ParseFlags(fs, args, help, "audit delete-garbage")

	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}

	for _, fi := range s.latestFilesWithPrefix(*dataDir, garbageFilePrefix) {
		if fi.Addr != s.Config.StoreEndpoint().NetAddr {
			// Only delete from the store endpoint of the current user.
			continue
		}
		garbage, err := s.readItems(fi.Path)
		if err != nil {
			s.Exit(err)
		}
		store, err := bind.StoreServer(s.Config, s.Config.StoreEndpoint())
		if err != nil {
			s.Exit(err)
		}
		const numWorkers = 10
		d := deleter{
			State: s,
			store: store,
			refs:  make(chan upspin.Reference),
			stop:  make(chan bool, numWorkers),
		}
		for i := 0; i < numWorkers; i++ {
			go d.worker()
		}
	loop:
		for ref := range garbage {
			if strings.HasPrefix(string(ref), rootRefPrefix) {
				// Don't ever collect root backups.
				continue
			}
			select {
			case d.refs <- ref:
			case <-d.stop:
				break loop
			}
		}
		close(d.refs)
	}
}

// deleter holds the state of delete-garbage workers.
type deleter struct {
	State *State
	store upspin.StoreServer
	refs  chan upspin.Reference
	stop  chan bool
}

// worker receives refs from refs and deletes them from store. If the store
// return a permission error then worker sends a value to stop.
func (d *deleter) worker() {
	for ref := range d.refs {
		err := d.store.Delete(ref)
		if err != nil {
			d.State.Fail(err)
			// Stop the entire process if we get a permission error;
			// we likely are running as the wrong user.
			if errors.Is(errors.Permission, err) {
				d.stop <- true
				return
			}
		}
	}
}
