// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

func (s *State) mkdir(args ...string) {
	const help = `
Mkdir creates Upspin directories.

The -glob flag can be set to false to have mkdir skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)
`
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	parent := fs.Bool("p", false, "Make all parent directories.")
	glob := globFlag(fs)
	s.ParseFlags(fs, args, help, "mkdir [-p] directory...")
	if fs.NArg() == 0 {
		usageAndExit(fs)
	}
	for _, name := range s.expandUpspin(fs.Args(), *glob) {
		s.doMkdir(name, *parent)
	}
}

func (s *State) doMkdir(name upspin.PathName, parent bool) {
	p, err := path.Parse(name)
	if err != nil {
		s.Exit(err)
	}
	if parent && p.NElem() > 0 {
		s.doMkdir(p.Drop(1).Path(), parent)
	}
	_, err = s.Client.MakeDirectory(name)
	if parent && errors.Match(errors.E(errors.Exist), err) {
		return
	}
	if err != nil {
		s.Exit(err)
	}
}
