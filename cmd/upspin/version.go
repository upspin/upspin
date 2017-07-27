// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"

	"upspin.io/version"
)

func (s *State) version(args ...string) {
	const help = `
Version prints a summary of the git version used to build the command.
`
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	s.ParseFlags(fs, args, help, "version")

	if fs.NArg() != 0 {
		usageAndExit(fs)
	}

	fmt.Fprint(os.Stdout, version.Version())
}
