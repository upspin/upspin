// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
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
	s.parseFlags(fs, args, help, "ls [-l] [path...]")

	done := map[upspin.PathName]bool{}
	if fs.NArg() == 0 {
		userRoot := upspin.PathName(s.config.UserName())
		rootEntry, err := s.DirServer(userRoot).Lookup(userRoot)
		if err != nil {
			s.exit(err)
		}
		s.list(rootEntry, done, *longFormat, *followLinks, *recur)
		return
	}
	// The done map marks a directory we have listed, so we don't recur endlessly
	// when given a chain of links with -L.
	for _, entry := range s.globAllUpspin(fs.Args()) {
		s.list(entry, done, *longFormat, *followLinks, *recur)
	}
}

func (s *State) list(entry *upspin.DirEntry, done map[upspin.PathName]bool, longFormat, followLinks, recur bool) {
	done[entry.Name] = true

	var dirContents []*upspin.DirEntry
	var err error
	if entry.IsDir() {
		dirContents, err = s.client.Glob(upspin.AllFilesGlob(entry.Name))
		if err != nil {
			s.exit(err)
		}
	} else {
		dirContents = []*upspin.DirEntry{entry}
	}

	if longFormat {
		printLongDirEntries(dirContents)
	} else {
		printShortDirEntries(dirContents)
	}

	if !recur {
		return
	}
	for _, entry := range dirContents {
		if entry.IsDir() && !done[entry.Name] {
			fmt.Printf("\n%s:\n", entry.Name)
			s.list(entry, done, longFormat, followLinks, recur)
		}
	}
}

func hasFinalSlash(name upspin.PathName) bool {
	return strings.HasSuffix(string(name), "/")
}

func printShortDirEntries(de []*upspin.DirEntry) {
	for _, e := range de {
		if e.IsDir() && !hasFinalSlash(e.Name) {
			fmt.Printf("%s/\n", e.Name)
		} else {
			fmt.Printf("%s\n", e.Name)
		}
	}
}

func printLongDirEntries(de []*upspin.DirEntry) {
	seqWidth := 2
	sizeWidth := 2
	for _, e := range de {
		s := fmt.Sprintf("%d", upspin.SeqVersion(e.Sequence))
		if seqWidth < len(s) {
			seqWidth = len(s)
		}
		s = fmt.Sprintf("%d", sizeOf(e))
		if sizeWidth < len(s) {
			sizeWidth = len(s)
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
		packer := lookupPacker(e)
		if packer != nil {
			packStr = packer.String()
		}
		fmt.Printf("%c %-6s %*d %*d %s [%s]\t%s%s\n",
			attrChar,
			packStr,
			seqWidth, upspin.SeqVersion(e.Sequence),
			sizeWidth, sizeOf(e),
			e.Time.Go().Local().Format("Mon Jan _2 15:04:05"),
			endpt,
			e.Name,
			redirect)
	}
}

func sizeOf(e *upspin.DirEntry) int64 {
	size, err := e.Size()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%q: %s\n", e.Name, err)
	}
	return size
}
