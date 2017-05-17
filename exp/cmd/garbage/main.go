// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Command garbage deletes GCS storeserver objects that are not
referenced by any of the user trees, including snapshots,
on the associated dirserver.

This is very preliminary and potentially catastrophic code.
It meets my needs for the moment but is expected to be
completely rewritten, so do not depend on it long term.
There may be buggy edge cases and no hope of recovery.
You have been warned.

Assuming upspin@example.org runs upspinserver-gcp on
upspin.example.org and has credentials in the default location,
   garbage -domain example.org
will generate the existing (pathname,reference) associations
and propose a list of objects to delete.
It is assumed that only upspin@example.org has read
permission on its root Upspin directory.

*/
package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
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
	var domain = flag.String("domain", "example.org", "upspin domain name")
	flag.Parse()

	loc := upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr("upspin." + *domain + ":443"),
		},
		Reference: "0000garbageList", // no collision with any sha256 reference
		// These temporary objects will be recognizable in the GCP Bucket Browser.
	}

	cfg, err := config.FromFile(filepath.Join(config.Home(), "upspin", "deploy", *domain, "config"))
	chk(err)
	transports.Init(cfg)

	// Storeserver creates list of all objects.
	store, err := bind.StoreServer(cfg, loc.Endpoint)
	chk(err)
	loc.Reference = upspin.Reference(fmt.Sprintf("%s%d", loc.Reference, time.Now().Unix()))
	err = store.Delete(loc.Reference[4:]) // Ugly hack; creates object loc.Reference.
	chk(err)

	// Dirserver creates list of all references in user trees, and diffs.
	dir, err := bind.DirServer(cfg, loc.Endpoint)
	chk(err)
	_, mess := dir.Lookup(upspin.PathName("upspin@" + *domain + "/" + string(loc.Reference)))
	if errors.Match(errors.E(errors.Private), mess) {
		log.Fatal(mess)
	}
	fmt.Println(mess)
}
