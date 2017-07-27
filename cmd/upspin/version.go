// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run make_version.go

package main

import (
	"flag"
	"fmt"
)

// These strings will be overwritten by an init function in
// created by make_version.go during the release process.
var (
	buildTime = ""
	gitSHA    = "devel"
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
	if buildTime != "" {
		fmt.Fprintf(s.Stdout, "Build time: %s\n", buildTime)
	}
	fmt.Fprintf(s.Stdout, "Git hash:   %s\n", gitSHA)
}
