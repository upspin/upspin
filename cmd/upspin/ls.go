// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"strings"

	"upspin.io/upspin"
)

func (s *State) ls(args ...string) {
	const help = `
Ls lists the names and, if requested, other properties of Upspin
files and directories. If given no path arguments, it lists the
user's root. By default ls does not follow links; use the -L flag
to learn about the targets of links.
`
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	followLinks := fs.Bool("L", false, "follow links")
	recur := fs.Bool("R", false, "recur into subdirectories")
	s.ParseFlags(fs, args, help, "ls [-l] [path...]")

	done := map[upspin.PathName]bool{}
	if fs.NArg() == 0 {
		userRoot := upspin.PathName(s.Config.UserName())
		rootEntry, err := s.DirServer(userRoot).Lookup(userRoot)
		if err != nil {
			s.Exit(err)
		}
		s.list(rootEntry, done, *longFormat, *followLinks, *recur)
		return
	}
	// The done map marks a directory we have listed, so we don't recur endlessly
	// when given a chain of links with -L.
	for _, entry := range s.GlobAllUpspin(fs.Args()) {
		s.list(entry, done, *longFormat, *followLinks, *recur)
	}
}

func (s *State) list(entry *upspin.DirEntry, done map[upspin.PathName]bool, longFormat, followLinks, recur bool) {
	done[entry.Name] = true

	var dirContents []*upspin.DirEntry
	var err error
	if entry.IsDir() {
		dirContents, err = s.Client.Glob(upspin.AllFilesGlob(entry.Name))
		if err != nil {
			s.Exit(err)
		}
	} else {
		dirContents = []*upspin.DirEntry{entry}
	}

	// Glob doesn't follow the final link. We may have to do so ourselves.
	if followLinks {
		for i, entry := range dirContents {
			if entry.IsLink() {
				e, err := s.Client.Lookup(entry.Link, false)
				if err != nil {
					s.Fail(err)
					continue
				}
				dirContents[i] = e
			}
		}
	}

	if longFormat {
		s.printLongDirEntries(dirContents)
	} else {
		s.printShortDirEntries(dirContents)
	}

	if !recur {
		return
	}
	for _, entry := range dirContents {
		if entry.IsDir() && !done[entry.Name] {
			s.Printf("\n%s:\n", entry.Name)
			s.list(entry, done, longFormat, followLinks, recur)
		}
	}
}

func hasFinalSlash(name upspin.PathName) bool {
	return strings.HasSuffix(string(name), "/")
}

func (s *State) printShortDirEntries(de []*upspin.DirEntry) {
	for _, e := range de {
		switch {
		case e.IsDir() && !hasFinalSlash(e.Name):
			s.Printf("%s/\n", e.Name)
		default:
			s.Printf("%s\n", e.Name)
		}
	}
}

func (s *State) printLongDirEntries(de []*upspin.DirEntry) {
	seqWidth := 2
	sizeWidth := 2
	for _, e := range de {
		str := fmt.Sprintf("%d", e.Sequence)
		if seqWidth < len(str) {
			seqWidth = len(str)
		}
		str = fmt.Sprintf("%d", s.sizeOf(e))
		if sizeWidth < len(str) {
			sizeWidth = len(str)
		}
	}
	for _, e := range de {
		redirect := ""
		attrChar := ' '
		if e.IsDir() {
			attrChar = 'd'
			if !hasFinalSlash(e.Name) {
				e.Name += "/"
			}
		}
		if e.IsLink() {
			attrChar = '>'
			redirect = " -> " + string(e.Link)
		}
		endpt := ""
		prevLoc := ""
		for i := range e.Blocks {
			loc := e.Blocks[i].Location.Endpoint.String()
			if loc == prevLoc {
				continue
			}
			prevLoc = loc
			if i > 0 {
				endpt += ","
			}
			endpt += loc
		}
		packStr := "?"
		packer := s.lookupPacker(e)
		if packer != nil {
			packStr = packer.String()
		}
		s.Printf("%c %-6s %*d %*d %s [%s]\t%s%s\n",
			attrChar,
			packStr,
			seqWidth, e.Sequence,
			sizeWidth, s.sizeOf(e),
			e.Time.Go().Local().Format("Mon Jan _2 15:04:05"),
			endpt,
			e.Name,
			redirect)
	}
}

func (s *State) sizeOf(e *upspin.DirEntry) int64 {
	size, err := e.Size()
	if err != nil {
		fmt.Fprintf(s.Stderr, "%q: %s\n", e.Name, err)
	}
	return size
}
