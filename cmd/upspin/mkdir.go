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

The -p flag can be set to have mkdir create any missing parent directories of
each argument.

The -glob flag can be set to false to have mkdir skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)
`
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	parent := fs.Bool("p", false, "make all parent directories")
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
	_, err = s.Client.MakeDirectory(name)
	if parent && p.NElem() > 0 && errors.Is(errors.NotExist, err) {
		s.doMkdir(p.Drop(1).Path(), true)
		s.doMkdir(name, false)
		return
	}
	if err != nil {
		s.Exit(err)
	}
}
