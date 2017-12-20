// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// TODO: add an option to back up blocks in cheap online storage

import (
	"flag"
	"os"
	"strings"
	"sync"

	"upspin.io/bind"
	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/disk"
	"upspin.io/errors"
	"upspin.io/upspin"
)

func (s *State) deleteGarbage(args []string) {
	const help = `
Audit delete-garbage deletes garbage blocks as listed by the most recent
run of find-garbage. It operates on the store endpoint of the current user.

It must be run as the same Upspin user as the store server itself,
as only that user has permission to delete blocks.

The -backup flag specifies a local directory in which to store local
copies of the blocks before they are deleted.

Misuse of this command may result in permanent data loss. Use with caution.
`
	fs := flag.NewFlagSet("delete-garbage", flag.ExitOnError)
	dataDir := dataDirFlag(fs)
	backupDir := fs.String("backup", "", "local directory in which to store deleted blocks")
	s.ParseFlags(fs, args, help, "audit delete-garbage")

	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}

	var backup storage.Storage
	if *backupDir != "" {
		var err error
		backup, err = disk.New(&storage.Opts{
			Opts: map[string]string{
				"basePath": *backupDir,
			},
		})
		if err != nil {
			s.Exit(err)
		}
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
			State:  s,
			store:  store,
			backup: backup,
			refs:   make(chan upspin.Reference),
			stop:   make(chan bool, numWorkers),
		}
		d.done.Add(numWorkers)
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
		d.done.Wait()
	}
}

// deleter holds the state of delete-garbage workers.
type deleter struct {
	State  *State
	store  upspin.StoreServer
	backup storage.Storage // skip backup if nil
	refs   chan upspin.Reference
	stop   chan bool
	done   sync.WaitGroup // zero when all workers return
}

// worker receives refs from refs and deletes them from store. If the store
// return a permission error then worker sends a value to stop.
func (d *deleter) worker() {
	defer d.done.Done()
	for ref := range d.refs {
		if d.backup != nil {
			b, _, _, err := d.store.Get(ref)
			if errors.Is(errors.NotExist, err) {
				continue
			}
			if err != nil {
				d.State.Fail(err)
				continue
			}
			if err := d.backup.Put(string(ref), b); err != nil {
				d.State.Fail(err)
				// Stop the entire process if we fail to write the
				// block locally, as future puts will likely fail also.
				d.stop <- true
				return
			}
		}
		err := d.store.Delete(ref)
		if errors.Is(errors.NotExist, err) {
			continue
		}
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
