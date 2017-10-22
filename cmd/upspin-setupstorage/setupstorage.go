// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The Upspin-setupstorage comamnd is an external upspin subcommand that
// executes the second step in establishing an upspinserver.
// Run upspin setupstorage -help for more information.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"upspin.io/subcmd"
)

type state struct {
	*subcmd.State
}

const help = `
Setupstorage is the second step in establishing an upspinserver,
It sets up storage for your Upspin installation.
The first step is 'setupdomain' and the final step is 'setupserver'.

This version of setupstorage configures local disk storage.
Read the documentation at
	https://upspin.io/doc/server_setup.md
for information on configuring upspinserver to use cloud storage services.
`

func main() {
	const name = "setupstorage"

	log.SetFlags(0)
	log.SetPrefix("upspin setupstorage: ")

	s := &state{
		State: subcmd.NewState(name),
	}

	configFlag := flag.String("config", "", "do not set; here only for consistency with other upspin commands")
	where := flag.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	domain := flag.String("domain", "", "domain `name` for this Upspin installation")
	storagePath := flag.String("path", "", "`directory` on the server in which to keep Upspin storage (default is $HOME/upspin/server/storage)")

	s.ParseFlags(flag.CommandLine, os.Args[1:], help,
		"setupstorage -domain=<name> -path=<storage_dir>")
	if *configFlag != "" {
		s.Exitf("the -config flag must not be set")
	}
	if *domain == "" {
		s.Exitf("the -domain flag must be provided")
	}

	cfgPath := filepath.Join(*where, *domain)
	cfg := s.ReadServerConfig(cfgPath)

	if *storagePath != "" {
		cfg.StoreConfig = []string{
			"backend=Disk",
			"basePath=" + *storagePath,
		}
	}
	s.WriteServerConfig(cfgPath, cfg)

	fmt.Fprintf(os.Stderr, "You should now deploy the upspinserver binary and run 'upspin setupserver'.\n")

	s.ExitNow()
}
