// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// I/O helpers.

package subcmd

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/path"
	"upspin.io/upspin"
)

// ReadAll reads all contents from a local input file or from stdin if
// the input file name is empty
func (s *State) ReadAll(fileName string) []byte {
	var input *os.File
	var err error
	if fileName == "" {
		input = os.Stdin
	} else {
		input = s.OpenLocal(fileName)
		defer input.Close()
	}

	data, err := ioutil.ReadAll(input)
	if err != nil {
		s.Exit(err)
	}
	return data
}

// OpenLocal opens a file on local disk.
func (s *State) OpenLocal(path string) *os.File {
	f, err := os.Open(path)
	if err != nil {
		s.Exit(err)
	}
	return f
}

// CreateLocal creates a file on local disk.
func (s *State) CreateLocal(path string) *os.File {
	f, err := os.Create(path)
	if err != nil {
		s.Exit(err)
	}
	return f
}

// MkdirLocal creates a directory on local disk.
// It requires all but the last element to be present.
func (s *State) MkdirLocal(path string) {
	err := os.Mkdir(path, 0700)
	if err != nil {
		s.Exit(err)
	}
}

// MkdirLocal creates a directory on local disk.
// It creates as much of the path as is necessary.
func (s *State) MkdirAllLocal(path string) {
	err := os.MkdirAll(path, 0700)
	if err != nil {
		s.Exit(err)
	}
}

// ShouldNotExist calls s.Exit if the file already exists.
func (s *State) ShouldNotExist(path string) {
	_, err := os.Stat(path)
	if err == nil {
		s.Exitf("%s already exists", path)
	}
	if !os.IsNotExist(err) {
		s.Exit(err)
	}
}

// HasGlobChar reports whether the string contains a Glob metacharacter.
func HasGlobChar(pattern string) bool {
	return strings.ContainsAny(pattern, `\*?[`)
}

// GlobAllUpspin processes the arguments, which should be Upspin paths,
// expanding glob patterns.
func (s *State) GlobAllUpspin(args []string) []*upspin.DirEntry {
	entries := make([]*upspin.DirEntry, 0, len(args))
	for _, arg := range args {
		entries = append(entries, s.GlobUpspin(arg)...)
	}
	return entries
}

// GlobAllUpspinPath processes the arguments, which should be Upspin paths,
// expanding glob patterns. It returns just the paths.
func (s *State) GlobAllUpspinPath(args []string) []upspin.PathName {
	paths := make([]upspin.PathName, 0, len(args))
	for _, arg := range args {
		paths = append(paths, s.GlobUpspinPath(arg)...)
	}
	return paths
}

// GlobUpspin glob-expands the argument, which must be a syntactically
// valid Upspin glob pattern (including a plain path name). If the path does
// not exist, the function exits.
func (s *State) GlobUpspin(pattern string) []*upspin.DirEntry {
	// Must be a valid Upspin path.
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		s.Exit(err)
	}
	// If it has no metacharacters, look it up to be sure it exists.
	if !HasGlobChar(pattern) {
		entry, err := s.Client.Lookup(upspin.PathName(pattern), true)
		if err != nil {
			s.Exit(err)
		}
		return []*upspin.DirEntry{entry}
	}
	entries, err := s.Client.Glob(parsed.String())
	if err != nil {
		s.Exit(err)
	}
	return entries
}

// GlobUpspinPath glob-expands the argument, which must be a syntactically
// valid Upspin glob pattern (including a plain path name). It returns just
// the path names.
func (s *State) GlobUpspinPath(pattern string) []upspin.PathName {
	// Note: We could call GlobUpspin but that might do an unnecessary Lookup.
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		s.Exit(err)
	}
	// If it has no metacharacters, leave it alone but clean it.
	if !HasGlobChar(pattern) {
		return []upspin.PathName{path.Clean(upspin.PathName(pattern))}
	}
	entries, err := s.Client.Glob(parsed.String())
	if err != nil {
		s.Exit(err)
	}
	names := make([]upspin.PathName, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

// GlobOneUpspin glob-expands the argument, which must result in a
// single Upspin path.
func (s *State) GlobOneUpspinPath(pattern string) upspin.PathName {
	entries := s.GlobUpspin(pattern)
	if len(entries) != 1 {
		s.Exitf("more than one file matches %s", pattern)
	}
	return entries[0].Name
}

// GlobOneUpspinNoLinks glob-expands the argument, which must result in a
// single Upspin path. The result must not be a link, but it's OK if it does not
// exist at all.
func (s *State) GlobOneUpspinNoLinks(pattern string) upspin.PathName {
	// Use Dir not Client to catch links.
	entries, err := s.DirServer(upspin.PathName(pattern)).Glob(pattern)
	if err == upspin.ErrFollowLink {
		s.Exitf("%s is a link", entries[0].Name)
	}
	if err != nil {
		s.Exit(err)
	}
	if len(entries) > 1 {
		s.Exitf("more than one file matches %s", pattern)
	}
	if len(entries) == 0 {
		// No matches; file does not exist. That's OK.
		return upspin.PathName(pattern)
	}
	return entries[0].Name
}

// GlobLocal glob-expands the argument, which should be a syntactically
// valid Glob pattern (including a plain file name).
func (s *State) GlobLocal(pattern string) []string {
	// If it has no metacharacters, leave it alone.
	if !HasGlobChar(pattern) {
		return []string{pattern}
	}
	strs, err := filepath.Glob(pattern)
	if err != nil {
		// Bad pattern, so treat as a literal.
		return []string{pattern}
	}
	return strs
}

// GlobOneLocal glob-expands the argument, which must result in a
// single local file name.
func (s *State) GlobOneLocal(pattern string) string {
	strs := s.GlobLocal(pattern)
	if len(strs) != 1 {
		s.Exitf("more than one file matches %s", pattern)
	}
	return strs[0]
}
