// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Command garbage deletes GCS storeserver objects that are not
referenced by any of the user trees, including snapshots,
currently on the associated dirserver.

This is very preliminary and potentially catastrophic code.
It meets my needs for the moment but is expected to be
completely rewritten, so not worth much polishing.
There may be buggy edge cases and no hope of recovery.
You have been warned.

Assuming upspin@example.org runs upspinserver-gcp on
upspin.example.org and has credentials in the default location,
   garbage -domain example.org
will display the existing (pathname,reference) associations
and propose a list of objects to delete.  If the list looks
sane, commit the deletions by
   garbage -domain example.org -boom
It is assumed that only upspin@example.org has read
permission on its root Upspin directory.

*/
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/transports"
	"upspin.io/upspin"
)

func chk(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	var boom = flag.Bool("boom", false, "delete lots of files")
	var domain = flag.String("domain", "example.org", "upspin domain name")
	flag.Parse()

	loc := upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr("upspin." + *domain + ":443"),
		},
		Reference: "0000garbageList",
	}

	cfg, err := config.FromFile(filepath.Join(config.Home(), "upspin", "deploy", *domain, "config"))
	chk(err)
	transports.Init(cfg)

	refs := make(map[string]bool)
	dir, err := bind.DirServer(cfg, loc.Endpoint)
	chk(err)
	_, mess := dir.Lookup(upspin.PathName("upspin@" + *domain + "/garbageListVerbose"))
	if errors.Match(errors.E(errors.Private), mess) {
		log.Fatal(mess)
	}
	fmt.Println(mess)
	scanner := bufio.NewScanner(strings.NewReader(mess.Error()))
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		ref := scanner.Text()
		refs[ref] = true
	}

	store, err := bind.StoreServer(cfg, loc.Endpoint)
	chk(err)
	loc.Reference = upspin.Reference(fmt.Sprintf("%s%d", loc.Reference, time.Now().Unix()))
	err = store.Delete(loc.Reference[4:]) // Ugly hack; creates object loc.Reference.
	chk(err)
	list, _, locs, err := store.Get(loc.Reference)
	chk(err)
	if len(locs) > 0 {
		log.Fatal("expected empty locs, got %v", locs)
	}
	scanner = bufio.NewScanner(bytes.NewReader(list))
	for scanner.Scan() {
		obj := scanner.Text()
		if !refs[obj] {
			fmt.Println("del ", obj)
			if *boom {
				err := store.Delete(upspin.Reference(obj))
				chk(err)
			}
		}
	}
	err = store.Delete(loc.Reference)
	chk(err)
}
