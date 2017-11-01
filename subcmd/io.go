// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// I/O helpers.

package subcmd

import (
	"io/ioutil"
	"os"
	osUser "os/user"
	"path/filepath"
	"strings"

	"upspin.io/config"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

var userLookup = osUser.Lookup

var home string // Main user's home directory.

func homeDir(who string) string {
	if who == "" {
		if home == "" {
			var err error
			home, err = config.Homedir()
			if err != nil {
				return "~" // What else can we do?
			}
		}
	}
	u, err := userLookup(who)
	if err != nil {
		return "~" + who // Again, what else can we do?
	}
	return u.HomeDir
}

// AtSign processes a leading at sign, if any, in the Upspin file name and replaces it
// with the current user name. The name must be strictly "@" or begin with "@/",
// possibly with a suffix ("@+snapshot", "@+camera/").
// (If the user name ready has a suffix and the file starts @+suffix,
// the returned user name appends the suffix from the argument.)
// Unlike Tilde, it does not look up others user's roots.
// The argument is of type string; once a file becomes an upspin.PathName it should
// not be passed to this function.
// If the file name does not begin with an at sign, AtSign returns the argument
// unchanged except for promotion to upspin.PathName.
// If the target user does not exist, it returns the original string.
func (s *State) AtSign(file string) upspin.PathName {
	if s.Config == nil || file == "" || file[0] != '@' {
		return upspin.PathName(file)
	}
	userStr := string(s.Config.UserName())
	if file == "@" {
		return upspin.PathName(userStr + "/")
	}
	if strings.HasPrefix(file, "@/") {
		return upspin.PathName(userStr + file[1:])
	}
	if strings.HasPrefix(file, "@+") {
		// Need to split the user name.
		usr, _, domain, err := user.Parse(s.Config.UserName())
		if err != nil { // Can't happen.
			return upspin.PathName(file)
		}
		slash := strings.IndexByte(file, '/')
		if slash < 0 {
			return upspin.PathName(usr + file[1:] + "@" + domain + "/")
		}
		return upspin.PathName(usr + file[1:slash] + "@" + domain + file[slash:])
	}
	return upspin.PathName(file)
}

// Tilde processes a leading tilde, if any, in the local file name.
// If the file name does not begin with a tilde, Tilde returns the argument unchanged.
// This special processing (only) is applied to all local file names passed to
// functions in this package.
// If the target user does not exist, it returns the original string.
func Tilde(file string) string {
	if file == "" || file[0] != '~' {
		return file
	}
	if file == "~" {
		return homeDir("")
	}
	slash := strings.IndexByte(file, '/')
	if slash < 0 {
		return homeDir(file[1:])
	}
	return filepath.Join(homeDir(file[1:slash]), file[slash+1:])
}

// ReadAll reads all contents from a local input file or from stdin if
// the input file name is empty
func (s *State) ReadAll(fileName string) []byte {
	if fileName == "" {
		data, err := ioutil.ReadAll(s.Stdin)
		if err != nil {
			s.Exit(err)
		}
		return data
	}
	input := s.OpenLocal(Tilde(fileName))
	defer input.Close()
	data, err := ioutil.ReadAll(input)
	if err != nil {
		s.Exit(err)
	}
	return data
}

// OpenLocal opens a file on local disk.
func (s *State) OpenLocal(path string) *os.File {
	f, err := os.Open(Tilde(path))
	if err != nil {
		s.Exit(err)
	}
	return f
}

// CreateLocal creates a file on local disk.
func (s *State) CreateLocal(path string) *os.File {
	f, err := os.Create(Tilde(path))
	if err != nil {
		s.Exit(err)
	}
	return f
}

// MkdirLocal creates a directory on local disk.
// It requires all but the last element to be present.
func (s *State) MkdirLocal(path string) {
	err := os.Mkdir(Tilde(path), 0700)
	if err != nil {
		s.Exit(err)
	}
}

// MkdirAllLocal creates a directory on local disk.
// It creates as much of the path as is necessary.
func (s *State) MkdirAllLocal(path string) {
	err := os.MkdirAll(Tilde(path), 0700)
	if err != nil {
		s.Exit(err)
	}
}

// ShouldNotExist calls s.Exit if the file already exists.
func (s *State) ShouldNotExist(path string) {
	_, err := os.Stat(Tilde(path))
	if err == nil {
		s.Exitf("%s already exists", path)
	}
	if !os.IsNotExist(err) {
		s.Exit(err)
	}
}

// HasGlobChar reports whether the string contains an unescaped Glob metacharacter.
func HasGlobChar(pattern string) bool {
	esc := false
	for _, r := range pattern {
		if esc {
			esc = false // TODO: What if next rune is '/'?
			continue
		}
		switch r {
		case '\\':
			esc = true
		case '*', '[', '?':
			return true
		}
	}
	return false
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
// not exist or the pattern matches no paths, the function exits.
func (s *State) GlobUpspin(pattern string) []*upspin.DirEntry {
	// Must be a valid Upspin path.
	pat := s.AtSign(pattern)
	parsed, err := path.Parse(pat)
	if err != nil {
		s.Exit(err)
	}
	// If it has no metacharacters, look it up to be sure it exists.
	if !HasGlobChar(string(pat)) {
		entry, err := s.Client.Lookup(pat, false)
		if err != nil {
			s.Exit(err)
		}
		return []*upspin.DirEntry{entry}
	}
	entries, err := s.Client.Glob(parsed.String())
	if err != nil {
		s.Exit(err)
	}
	if len(entries) == 0 {
		s.Exitf("no path matches %q", parsed)
	}
	return entries
}

// GlobUpspinPath glob-expands the argument, which must be a syntactically
// valid Upspin glob pattern (including a plain path name). It returns just
// the path names. If the pattern matches no paths, the function exits.
func (s *State) GlobUpspinPath(pattern string) []upspin.PathName {
	// Note: We could call GlobUpspin but that might do an unnecessary Lookup.
	pat := s.AtSign(pattern)
	parsed, err := path.Parse(pat)
	if err != nil {
		s.Exit(err)
	}
	// If it has no metacharacters, leave it alone but clean it.
	if !HasGlobChar(string(pat)) {
		return []upspin.PathName{parsed.Path()}
	}
	entries, err := s.Client.Glob(parsed.String())
	if err != nil {
		s.Exit(err)
	}
	if len(entries) == 0 {
		s.Exitf("no path matches %q", parsed)
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
		s.Exitf("more than one file matches %q", pattern)
	}
	return entries[0].Name
}

// GlobLocal glob-expands the argument, which should be a syntactically
// valid Glob pattern (including a plain file name). If the pattern is erroneous
// or matches no files, the function exits.
func (s *State) GlobLocal(pattern string) []string {
	pattern = Tilde(pattern)
	// If it has no metacharacters, leave it alone.
	if !HasGlobChar(pattern) {
		return []string{pattern}
	}
	strs, err := filepath.Glob(pattern)
	if err != nil {
		// Bad pattern.
		s.Exitf("bad local Glob pattern %q: %v", pattern, err)
	}
	if len(strs) == 0 {
		s.Exitf("no path matches %q", pattern)
	}
	return strs
}

// GlobOneLocal glob-expands the argument, which must result in a
// single local file name. If not, it exits.
func (s *State) GlobOneLocal(pattern string) string {
	strs := s.GlobLocal(pattern)
	switch len(strs) {
	case 0:
		s.Exitf("no path matches %q", pattern)
	case 1:
		// OK.
	default:
		s.Exitf("more than one file matches %q", pattern)
	}
	return strs[0]
}
