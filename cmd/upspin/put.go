// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"

	"upspin.io/access"
	"upspin.io/client"
	"upspin.io/config"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/subcmd"
)

func (s *State) put(args ...string) {
	const help = `
Put writes its input to the store server and installs a directory
entry with the given path name to refer to the data.

The -glob flag can be set to false to have put skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)
`
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	packing := fs.String("packing", "", "packing to use (default from user's config)")
	glob := globFlag(fs)
	s.ParseFlags(fs, args, help, "put [-in=inputfile] path")

	if fs.NArg() != 1 {
		usageAndExit(fs)
	}

	data := s.ReadAll(*inFile)
	// Must be a valid Upspin name.
	parsed, err := path.Parse(s.AtSign(fs.Arg(0)))
	if err != nil {
		s.Exit(err)
	}
	name := parsed.Path()
	if *glob && subcmd.HasGlobChar(parsed.String()) {
		// If there is a metacharacter in the last element, the whole path
		// must exist. Otherwise, only the path up to the last element (its
		// directory) must exist. We call Glob appropriately.
		if subcmd.HasGlobChar(parsed.Elem(parsed.NElem() - 1)) {
			dir := s.GlobOneUpspinPath(parsed.Drop(1).String())
			name = path.Join(dir, parsed.Elem(parsed.NElem()-1))
		} else {
			name = s.GlobOneUpspinPath(parsed.String())
		}
	}
	cl := s.Client
	if *packing != "" {
		p := pack.LookupByName(*packing)
		if p == nil {
			s.Exitf("no such packing %q", *packing)
		}
		cl = client.New(config.SetPacking(s.Config, p.Packing()))
	}
	_, err = cl.Put(name, data)
	if err != nil {
		s.Exit(err)
	}
	// If this is an Access or Group file, need to remove any stored info about it.
	if access.IsAccessControlFile(name) {
		// It's cached in the Sharer, so just wipe that.
		s.sharer = newSharer(s)
		// If this is a Group file, there is also information within the access package.
		if access.IsGroupFile(name) {
			_ = access.RemoveGroup(name) // Ignore errors; file might not be cached.
		}
	}
}
