// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Share is a utility for checking and updating wrapped keys for encrypted items.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers
	"upspin.io/pack/ee"
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
	force bool
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
	userKeys map[upspin.UserName][]upspin.PublicKey

	// userByHash maps the SHA-256 hashes of each user's key to the user name.
	userByHash map[[sha256.Size]byte]upspin.UserName
}

// do is the main function for the share subcommand.
func (s *sharer) do() {
	// Validate names quickly before grabbing a context.
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

	s.userKeys = make(map[upspin.UserName][]upspin.PublicKey)
	s.userByHash = make(map[[sha256.Size]byte]upspin.UserName)

	// Now we're ready. First show the state if asked.
	if !s.quiet {
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
			fmt.Printf("\nfiles readable by:\n%s:\n", users)
			sort.Strings(names)
			for _, name := range names {
				fmt.Printf("\t%s\n", name)
			}
		}
	}

	var entriesToFix []*upspin.DirEntry

	// Identify the entries we need to update.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if s.force {
			entriesToFix = append(entriesToFix, entry)
			continue
		}
		users := s.users[path.DropPath(entry.Name, 1)]
		for _, user := range users {
			s.lookupKey(user)
		}
		packer := lookupPacker(entry)
		if packer.Packing() == upspin.PlainPack {
			continue
		}
		hashes, err := packer.ReaderHashes(entry.Metadata.Packdata)
		if err != nil {
			fmt.Fprintf(os.Stderr, "looking up users for %q: %s", entry.Name, err)
			continue
		}
		// hashUsers is the pretty-printed string of user names recovered from
		// looking at the list of hashed keys in the packdata. It makes for an
		// easy comparison with the return value from userListToString.
		var hashUsers string
		unknownUser := false
		for _, hash := range hashes {
			var thisUser upspin.UserName
			switch packer.Packing() {
			case upspin.EEp256Pack, upspin.EEp384Pack, upspin.EEp521Pack, upspin.Ed25519Pack:
				if len(hash) != sha256.Size {
					fmt.Fprintf(os.Stderr, "%q hash size is %d; expected %d", entry.Name, len(hash), sha256.Size)
					s.exitCode = 1
					continue
				}
				var h [sha256.Size]byte
				copy(h[:], hash)
				var ok bool
				thisUser, ok = s.userByHash[h]
				if !ok && !unknownUser {
					// We have a key but no user with that key is known to us.
					// This means an access change has removed permissions for some user
					// but if that user still has the reference, the user could read the file.
					// Someone should run upspin share -fix soon to repair the packing.
					unknownUser = true
					fmt.Fprintf(os.Stderr, "%q: cannot find user for key(s); rerun with -fix\n", entry.Name)
					s.exitCode = 1
					continue
				}
			default:
				fmt.Fprintf(os.Stderr, "%q: unrecognized packing %s", entry.Name, packer)
				continue
			}
			if hashUsers != "" {
				hashUsers += " "
			}
			hashUsers += string(thisUser)
		}
		userList := userListToString(users)
		if userList != hashUsers {
			if !s.quiet {
				if len(entriesToFix) == 0 {
					fmt.Println("\nAccess discrepancies:")
				}
				fmt.Printf("\n%s:\n", entry.Name)
				fmt.Printf("\tAccess: %s\n", userList)
				fmt.Printf("\tKeys:   %s\n", hashUsers)
			}
			entriesToFix = append(entriesToFix, entry)
		}
	}

	// Repair the wrapped keys if necessary and requested.
	if s.fix {
		// Now repair them.
		for _, e := range entriesToFix {
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
	directory, err := bind.Directory(s.context, s.context.Directory)
	if err != nil {
		exit(err)
	}
	for i := 0; i < s.fs.NArg(); i++ {
		name := upspin.PathName(s.fs.Arg(i))
		entry, err := directory.Lookup(name)
		if err != nil {
			exitf("lookup %q: %s", name, err)
		}
		if !entry.IsDir() && !entry.IsLink() {
			entries = append(entries, entry)
			continue
		}
		if !s.isDir {
			exitf("%q is a directory; use -r or -d", name)
		}
		if entry.IsDir() || lookupPacker(entry) != nil {
			// Only work on entries we can pack. Those we can't will be logged.
			entries = append(entries, s.entriesFromDirectory(entry.Name)...)
		}
	}
	return entries
}

// entriesFromDirectory returns the list of all entries in the directory, recursively if required.
func (s *sharer) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory.
	directory, err := bind.Directory(s.context, s.context.Directory)
	if err != nil {
		exit(err)
	}
	thisDir, err := directory.Glob(string(dir) + "/*")
	if err != nil {
		exitf("globbing %q: %s", dir, err)
	}
	entries := make([]*upspin.DirEntry, 0, len(thisDir))
	// Add plain files.
	for _, e := range thisDir {
		if !e.IsDir() && !e.IsLink() {
			if lookupPacker(e) != nil {
				// Only work on entries we can pack. Those we can't will be logged.
				entries = append(entries, e)
			}
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

// lookupPacker returns the Packer implementation for the entry, or
// nil if none is available.
func lookupPacker(entry *upspin.DirEntry) upspin.Packer {
	if entry.IsDir() {
		// Directories are not packed.
		return nil
	}
	if len(entry.Metadata.Packdata) == 0 {
		fmt.Fprintf(os.Stderr, "%q has no packdata\n", entry.Name)
	}
	packing := upspin.Packing(entry.Metadata.Packdata[0])
	packer := pack.Lookup(packing)
	if packer == nil {
		fmt.Fprintf(os.Stderr, "%q has no registered packer for %d; ignoring\n", entry.Name, packing)
	}
	return packer
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
	directory, err := bind.Directory(s.context, s.context.Directory)
	if err != nil {
		exit(err)
	}
	which, err := directory.WhichAccess(name)
	if err != nil {
		exitf("looking up access file %q: %s", name, err)
	}
	var a *access.Access
	if which == "" {
		a, err = access.New(name)
	} else {
		a, err = access.Parse(which, read(s.client, which))
	}
	if err != nil {
		exitf("parsing access file %q: %s", name, err)
	}
	s.accessFiles[name] = a
	s.users[name] = s.usersWithReadAccess(a)
}

// usersWithReadAccess returns the list of user names granted read access by this access file.
func (s *sharer) usersWithReadAccess(a *access.Access) []upspin.UserName {
	userList, err := a.Users(access.Read, s.client.Get)
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
	directory, err := bind.Directory(s.context, s.context.Directory)
	if err != nil {
		exit(err)
	}
	entry, err := directory.Lookup(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "looking up %q: %s", name, err)
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
	packer := lookupPacker(entry) // Won't be nil.
	switch packer.Packing() {
	case upspin.EEp256Pack, upspin.EEp384Pack, upspin.EEp521Pack, upspin.Ed25519Pack:
		// Will repack below.
	default:
		if !s.quiet {
			fmt.Fprintf(os.Stderr, "%q has %s packing, does not need wrapped keys\n", name, packer)
		}
		return
	}
	// Could do this more efficiently, calling Share collectively, but the Puts are sequential anyway.
	keys := make([]upspin.PublicKey, 0, len(users))
	for _, user := range users {
		userKeys := s.lookupKey(user)
		for _, key := range userKeys {
			if len(key) > 0 {
				// TODO: Make this general. This works now only because we are always using ee.
				if ee.IsValidKeyForPacker(key, packer.String()) {
					keys = append(keys, key)
					break
				}
			}
		}
		fmt.Fprintf(os.Stderr, "%q: user %q has no key for packing %s\n", entry.Name, user, packer)
		s.exitCode = 1
	}
	packdatas := []*[]byte{&entry.Metadata.Packdata}
	packer.Share(s.context, keys, packdatas)
	if packdatas[0] == nil {
		fmt.Fprintf(os.Stderr, "packing skipped for %q\n", entry.Name)
		s.exitCode = 1
		return
	}
	err = directory.Put(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error putting entry back for %q: %s\n", name, err)
		s.exitCode = 1
	}
}

// lookupKey returns the public key for the user.
func (s *sharer) lookupKey(user upspin.UserName) []upspin.PublicKey {
	keys, ok := s.userKeys[user] // We use an empty (zero-valued) key to cache failed lookups.
	if ok {
		return keys
	}
	userService, err := bind.User(s.context, s.context.User)
	if err != nil {
		exit(err)
	}
	_, keys, err = userService.Lookup(user)
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't find key for %q: %s\n", user, err)
		s.exitCode = 1
		s.userKeys[user] = nil
		return nil
	}
	// Remember the lookup, failed or otherwise.
	if len(keys) == 0 {
		fmt.Fprintf(os.Stderr, "no key for %q\n", user)
		s.exitCode = 1
		s.userKeys[user] = nil
		return nil
	}
	s.userKeys[user] = keys
	for _, key := range keys {
		s.userByHash[sha256.Sum256([]byte(key))] = user
	}
	return keys
}
