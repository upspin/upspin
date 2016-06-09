// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Share is a utility for checking and updating wrapped keys for encrypted items.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"upspin.io/access"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/directory/transports"
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"
)

// sharer holds the state for the share calculation. It holds some caches to
// avoid calling on the server too much.
type sharer struct {
	// Flags.
	fs    *flag.FlagSet
	fix   bool
	isDir bool
	recur bool
	quiet bool

	exitCode int // Exit with non-zero status for minor problems.

	context *upspin.Context
	client  upspin.Client

	// accessFiles contains the parsed Access files, keyed by directory to which it applies.
	accessFiles map[upspin.PathName]*access.Access

	// users caches per-directory user lists computed from Access files.
	users map[upspin.PathName][]upspin.UserName

	// userKeys holds the keys we've looked up for each user. We remember
	// only the zeroth element of each key returned by User.Lookup.
	userKeys map[upspin.UserName]upspin.PublicKey
}

// do is the main function for the share subcommand.
func (s *sharer) do() {
	// Validate names quickly before grabbing a context. Avoids
	// a slow start if there's a simple typo.
	for i := 0; i < s.fs.NArg(); i++ {
		name := upspin.PathName(s.fs.Arg(i))
		_, err := path.Parse(name)
		if err != nil {
			exitf("%q: %s", name, err)
		}
	}

	context, err := context.InitContext(nil)
	if err != nil {
		exitf("initializing context: %s", err)
	}
	s.context = context
	s.client = client.New(context)

	// To change things, User must be the owner of every file.
	// (We just parsed them all, but that was before we had a context.)
	if s.fix {
		for i := 0; i < s.fs.NArg(); i++ {
			name := upspin.PathName(s.fs.Arg(i))
			parsed, _ := path.Parse(name)
			if parsed.User() != s.context.UserName {
				exitf("%q: %q is not owner", name, s.context.UserName)
			}
		}
	}

	// Files parse. Get the list of all directory entries we care about.
	entries := s.allEntries()

	// Collect the access files. We need only one per directory.
	s.accessFiles = make(map[upspin.PathName]*access.Access)
	s.users = make(map[upspin.PathName][]upspin.UserName)
	for _, e := range entries {
		s.addAccess(e)
	}

	// Now we're ready. First show the state if asked.
	if !s.quiet {
		// TODO: Show state of wrapped readers.
		// Compute the strings for all the user lists. This will de-dup the user lists.
		uNames := make(map[string][]string)
		for _, u := range s.users {
			uNames[userListToString(u)] = nil
		}
		// Now group the files that match each user list.
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			users := userListToString(s.users[path.DropPath(entry.Name, 1)])
			uNames[users] = append(uNames[users], string(entry.Name))
		}
		fmt.Println("Read permissions defined by Access files:")
		for users, names := range uNames {
			fmt.Printf("\nfiles readable by: %s:\n", users)
			sort.Strings(names)
			for _, name := range names {
				fmt.Printf("\t%s\n", name)
			}
		}
	}

	// Repair the wrapped keys if requested.
	if s.fix {
		s.userKeys = make(map[upspin.UserName]upspin.PublicKey)
		// Now repair them. TODO: Don't repair if the wrapped keys are already correct.
		for _, e := range entries {
			name := e.Name
			if !e.IsDir() {
				s.fixShare(name, s.users[path.DropPath(name, 1)])
			}
		}
	}

	os.Exit(s.exitCode)
}

func userListToString(userList []upspin.UserName) string {
	userString := fmt.Sprint(userList)
	return userString[1 : len(userString)-1]
}

// allEntries expands the arguments to find all the DirEntries identifying items to examine.
func (s *sharer) allEntries() []*upspin.DirEntry {
	var entries []*upspin.DirEntry
	for i := 0; i < s.fs.NArg(); i++ {
		name := upspin.PathName(s.fs.Arg(i))
		entry, err := s.context.Directory.Lookup(name)
		if err != nil {
			exitf("lookup %q: %s", name, err)
		}
		if !entry.IsDir() && !entry.IsLink() {
			entries = append(entries, entry)
			continue
		}
		if !s.isDir && !s.recur {
			exitf("%q is a directory; use -r or -d", name)
		}
		entries = append(entries, s.entriesFromDirectory(entry.Name)...)
	}
	return entries
}

// entriesFromDirectory returns the list of all entries in the directory, recursively if required.
func (s *sharer) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory.
	thisDir, err := s.context.Directory.Glob(string(dir) + "/*")
	if err != nil {
		exitf("globbing %q: %s", dir, err)
	}
	entries := make([]*upspin.DirEntry, 0, len(thisDir))
	// Add plain files.
	for _, e := range thisDir {
		if !e.IsDir() && !e.IsLink() {
			entries = append(entries, e)
		}
	}
	if s.recur {
		// Recur into subdirectories.
		for _, e := range thisDir {
			if e.IsDir() {
				entries = append(entries, s.entriesFromDirectory(e.Name)...)
			}
		}
	}
	return entries
}

// addAccess loads an access file.
func (s *sharer) addAccess(entry *upspin.DirEntry) {
	name := entry.Name
	if !entry.IsDir() {
		name = path.DropPath(name, 1) // Directory name for this file.
	}
	if _, ok := s.accessFiles[name]; ok {
		return
	}
	which, err := s.context.Directory.WhichAccess(name)
	if err != nil {
		exitf("looking up access file %q: %s", name, err)
	}
	a, err := access.Parse(which, read(s.client, which))
	if err != nil {
		exitf("parsing access file %q: %s", name, err)
	}
	s.accessFiles[name] = a
	s.users[name] = s.usersWithReadAccess(a)
}

// usersWithReadAccess returns the list of user names granted read access by this access file.
func (s *sharer) usersWithReadAccess(a *access.Access) []upspin.UserName {
	userList, err := a.AllUsers(access.Read, s.client.Get)
	if err != nil {
		exitf("getting user list: %s", err)
	}
	return userList
}

// read returns the contents of the file. It exits if the file cannot be read.
func read(c upspin.Client, file upspin.PathName) []byte {
	fd, err := c.Open(file)
	if err != nil {
		exitf("opening file: %s", err)
	}
	defer fd.Close()
	data, err := ioutil.ReadAll(fd)
	if err != nil {
		exitf("reading %q: %s", file, err)
	}
	return data
}

// fixShare updates the packdata of the named file to contain wrapped keys for all the users.
func (s *sharer) fixShare(name upspin.PathName, users []upspin.UserName) {
	entry, err := s.context.Directory.Lookup(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looking up %q:", name, err)
		s.exitCode = 1
		return
	}
	if entry.IsDir() {
		exitf("internal error: fixShare called on directory %q", name)
	}
	if len(entry.Metadata.Packdata) == 0 {
		fmt.Fprintf(os.Stderr, "%q has no packdata; ignoring\n", name)
		return
	}
	packing := upspin.Packing(entry.Metadata.Packdata[0])
	packer := pack.Lookup(packing)
	if packer == nil {
		fmt.Fprintf(os.Stderr, "%q has no registered packer for %d; ignoring\n", name, packing)
		return
	}
	switch packing {
	case upspin.EEp256Pack, upspin.EEp384Pack, upspin.EEp521Pack, upspin.Ed25519Pack:
		// Will repack below.
	default:
		if !s.quiet {
			fmt.Fprintf(os.Stderr, "%q has %s packing, does not need wrapped keys\n", name, packer)
		}
		return
	}
	// TODO: Could do this more efficiently, calling Share collectively, but the Puts are sequential anyway.
	keys := make([]upspin.PublicKey, 0, len(users))
	for _, user := range users {
		key := s.lookupKey(user)
		if len(key) > 0 {
			keys = append(keys, key)
		}
	}
	packdatas := []*[]byte{&entry.Metadata.Packdata}
	packer.Share(s.context, keys, packdatas)
	if packdatas[0] == nil {
		fmt.Fprintf(os.Stderr, "packing skipped for %q\n", entry.Name)
		s.exitCode = 1
		return
	}
	err = s.context.Directory.Put(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error putting entry back for %q: %s\n", name, err)
		s.exitCode = 1
	}
}

// lookupKey returns the public key for the user.
func (s *sharer) lookupKey(user upspin.UserName) upspin.PublicKey {
	key, ok := s.userKeys[user] // We use an empty (zero-valued) key to cache failed lookups.
	if ok {
		return key
	}
	_, keys, err := s.context.User.Lookup(user)
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't find key for %q: %s\n", user, err)
		s.exitCode = 1
	}
	if len(keys) == 0 {
		fmt.Fprintf(os.Stderr, "no key for %q: %s\n", user)
		s.exitCode = 1
	}
	// Remember the lookup, failed or otherwise.
	// TODO: We need to deal with multiple key types, and finding the right one.
	// TODO: This may be different for each file type, but for now we're only using
	// one encryption protocol so it will serve for now.
	// TODO: See ee.IsValidKeyForPacker and make it general.
	fmt.Println(user, len(keys))
	s.userKeys[user] = keys[0]
	return keys[0]
}
