// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"upspin.io/bind"
	"upspin.io/upspin"
)

// This file implements the storage scan.

func (s *State) scanStore(args []string) {
	const help = `
Audit scan-store produces a list of references to the blocks held
by the given store server.
By default it scans the store endpoint specified by the given config.

The list is written to a file named "store_EP_TS" in the directory nominated
by -data, where "EP" is the store endpoint and "TS" is the current time.

It must be run as the same Upspin user as the store server itself,
as only that user has permission to list references.
`

	fs := flag.NewFlagSet("scan-store", flag.ExitOnError)
	endpointFlag := fs.String("endpoint", string(s.Config.StoreEndpoint().NetAddr), "network `address` of storage server; default is from config")
	dataDir := dataDirFlag(fs)
	s.ParseFlags(fs, args, help, "audit scan-store [-endpoint <storeserver address>]")

	if fs.NArg() != 0 { // "audit scan-store help" is covered by this.
		fs.Usage()
		os.Exit(2)
	}

	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		s.Exit(err)
	}

	endpoint, err := upspin.ParseEndpoint("remote," + *endpointFlag)
	if err != nil {
		s.Exit(err)
	}

	now := time.Now()

	store, err := bind.StoreServer(s.Config, *endpoint)
	if err != nil {
		s.Fail(err)
		return
	}
	var (
		token string
		sum   int64
		items []refInfo
	)
	for {
		b, _, _, err := store.Get(upspin.ListRefsMetadata + upspin.Reference(token))
		if err != nil {
			s.Exit(err)
			return
		}
		var refs upspin.ListRefsResponse
		err = json.Unmarshal(b, &refs)
		if err != nil {
			s.Exit(err)
			return
		}
		for _, ri := range refs.Refs {
			sum += ri.Size
			items = append(items, refInfo{
				Ref:  ri.Ref,
				Size: ri.Size,
			})
		}
		token = refs.Next
		if token == "" {
			break
		}
	}
	fmt.Printf("%s: %d bytes total (%s) in %d references\n", endpoint.NetAddr, sum, ByteSize(sum), len(items))
	file := filepath.Join(*dataDir, fmt.Sprintf("%s%s_%d", storeFilePrefix, endpoint.NetAddr, now.Unix()))
	s.writeItems(file, items)
}
