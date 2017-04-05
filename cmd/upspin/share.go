// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Share has utility functions for checking and updating wrapped keys for encrypted items.

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/subcmd"
	"upspin.io/upspin"
)

func (s *State) share(args ...string) {
	const help = `
Share reports the user names that have access to each of the argument
paths, and what access rights each has. If the access rights do not
agree with the keys stored in the directory metadata for a path,
that is also reported. Given the -fix flag, share updates the keys
to resolve any such inconsistency. Given both -fix and -force, it
updates the keys regardless. The -d and -r flags apply to directories;
-r states whether the share command should descend into subdirectories.

For the rare case of a world-readable ("read:all") file that is encrypted,
the -unencryptforall flag in combination with -fix will rewrite the file
using the EEIntegrity packing, decrypting it and making its contents
visible to anyone.

See the description for rotate for information about updating keys.
`
	fs := flag.NewFlagSet("share", flag.ExitOnError)
	fix := fs.Bool("fix", false, "repair incorrect share settings")
	force := fs.Bool("force", false, "replace wrapped keys regardless of current state")
	isDir := fs.Bool("d", false, "do all files in directory; path must be a directory")
	recur := fs.Bool("r", false, "recur into subdirectories; path must be a directory. assumes -d")
	unencryptForAll := fs.Bool("unencryptforall", false, "for currently encrypted read:all files only, rewrite using EEIntegrity; requires -fix or -force")
	fs.Bool("q", false, "suppress output. Default is to show state for every file")
	s.ParseFlags(fs, args, help, "share path...")
	if fs.NArg() == 0 {
		fs.Usage()
	}

	if *recur {
		*isDir = true
	}
	if *force {
		*fix = true
	}
	if *unencryptForAll && !*fix {
		s.Exitf("-unencryptforall requires -fix or -force")
	}
	s.shareCommand(fs)
}

// Sharer holds the state for the share calculation. It holds some caches to
// avoid calling on the server too much.
type Sharer struct {
	state *State

	// Flags.
	fix             bool
	force           bool
	isDir           bool
	recur           bool
	quiet           bool
	unencryptForAll bool

	// accessFiles contains the parsed Access files, keyed by directory to which it applies.
	accessFiles map[upspin.PathName]*access.Access

	// users caches per-directory user lists computed from Access files.
	users map[upspin.PathName][]upspin.UserName

	// userKeys holds the keys we've looked up for each user.
	userKeys map[upspin.UserName]upspin.PublicKey

	// userByHash maps the SHA-256 hashes of each user's key to the user name.
	userByHash map[[sha256.Size]byte]upspin.UserName
}

func newSharer(s *State) *Sharer {
	return &Sharer{
		state:       s,
		accessFiles: make(map[upspin.PathName]*access.Access),
		users:       make(map[upspin.PathName][]upspin.UserName),
		userKeys:    make(map[upspin.UserName]upspin.PublicKey),
		userByHash:  make(map[[sha256.Size]byte]upspin.UserName),
	}
}

// shareCommand is the main function for the share subcommand.
func (s *State) shareCommand(fs *flag.FlagSet) {
	names := s.GlobAllUpspinPath(fs.Args())
	s.sharer.fix = subcmd.BoolFlag(fs, "fix")
	s.sharer.force = subcmd.BoolFlag(fs, "force")
	s.sharer.isDir = subcmd.BoolFlag(fs, "d")
	s.sharer.recur = subcmd.BoolFlag(fs, "r")
	s.sharer.quiet = subcmd.BoolFlag(fs, "q")
	s.sharer.unencryptForAll = subcmd.BoolFlag(fs, "unencryptforall")
	// To change things, User must be the owner of every file.
	if s.sharer.fix {
		for _, name := range names {
			parsed, _ := path.Parse(name)
			if parsed.User() != s.Config.UserName() {
				s.Exitf("%q: %q is not owner", name, s.Config.UserName())
			}
		}
	}

	// Files parse. Get the list of all directory entries we care about.
	entries := s.sharer.allEntries(names)

	// Collect the access files. We need only one per directory.
	for _, e := range entries {
		s.sharer.addAccess(e)
	}

	// Now we're ready. First show the state if asked.
	if !s.sharer.quiet {
		uNames := make(map[string][]string)
		for _, u := range s.sharer.users {
			uNames[userListToString(u)] = nil
		}
		// Now group the files that match each user list.
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			users := userListToString(s.sharer.users[path.DropPath(entry.Name, 1)])
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
	printedDiscrepancyHeader := true

	// Identify the entries we need to update.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if s.sharer.force {
			entriesToFix = append(entriesToFix, entry)
			continue
		}
		packer := lookupPacker(entry)
		if packer.Packing() == upspin.PlainPack || packer.Packing() == upspin.EEIntegrityPack {
			continue
		}
		users, keyUsers, self, err := s.sharer.readers(entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "looking up users for %q: %s", entry.Name, err)
			continue
		}
		if !s.sharer.quiet && !s.sharer.fix {
			// Check whether readers include "all", because we have encryption but
			// no way to get all the world's keys. If -fix is set, we'll report below.
			for _, user := range users {
				if user == access.AllUsers {
					fmt.Fprintf(os.Stderr, "%s:\n\tWarning: file readable by \"all\" but encrypted. Cannot add keys.", entry.Name)
					fmt.Fprintf(os.Stderr, "\n\tTo decrypt and make readable by \"all\", run share -fix -unencryptforall %q", entry.Name)
					s.ExitCode = 1
					break
				}
			}
		}
		userList := userListToString(users)
		if userList != keyUsers || self {
			if !s.sharer.quiet || !s.sharer.fix {
				if !printedDiscrepancyHeader {
					fmt.Fprintln(os.Stderr, "\nDiscrepancies between users in Access files and users in wrapped keys:")
					printedDiscrepancyHeader = true
				}
				fmt.Fprintf(os.Stderr, "\n%s:\n", entry.Name)
				fmt.Fprintf(os.Stderr, "\tAccess: %s\n", userList)
				fmt.Fprintf(os.Stderr, "\tKeys:   %s\n", keyUsers)
			}
			entriesToFix = append(entriesToFix, entry)
		}
	}

	// Repair the wrapped keys if necessary and requested.
	if s.sharer.fix {
		// Now repair them.
		for _, e := range entriesToFix {
			name := e.Name
			if !e.IsDir() {
				s.sharer.fixShare(name, s.sharer.users[path.DropPath(name, 1)])
			}
		}
	}
}

// readers returns two lists, the list of users with access according to the
// access file, and the the pretty-printed string of user names recovered from
// looking at the list of hashed keys in the packdata.
// It also returns a boolean reporting whether key rewrapping is needed for self.
func (s *Sharer) readers(entry *upspin.DirEntry) ([]upspin.UserName, string, bool, error) {
	self := false
	if entry.IsDir() {
		// Directories don't have readers.
		return nil, "", self, nil
	}
	users := s.users[path.DropPath(entry.Name, 1)]
	for _, user := range users {
		s.lookupKey(user)
	}
	packer := lookupPacker(entry)
	if packer == nil {
		return users, "", self, errors.Errorf("no packer registered for packer %s", entry.Packing)
	}
	if packer.Packing() != upspin.EEPack { // TODO: add new sharing packers here.
		return users, "", self, nil
	}
	hashes, err := packer.ReaderHashes(entry.Packdata)
	if err != nil {
		return nil, "", self, err
	}
	var keyUsers string
	unknownUser := false
	for _, hash := range hashes {
		var thisUser upspin.UserName
		switch packer.Packing() {
		case upspin.EEPack:
			if len(hash) != sha256.Size {
				fmt.Fprintf(os.Stderr, "%q hash size is %d; expected %d", entry.Name, len(hash), sha256.Size)
				s.state.ExitCode = 1
				continue
			}
			var h [sha256.Size]byte
			copy(h[:], hash)
			log.Debug.Printf("wrap %s %x\n", entry.Name, h)
			var ok bool
			thisUser, ok = s.userByHash[h]
			if !ok {
				// Check old keys in Factotum.
				f := s.state.Config.Factotum()
				if f == nil {
					s.state.Exitf("no factotum available")
				}
				if _, err := f.PublicKeyFromHash(hash); err == nil {
					thisUser = s.state.Config.UserName()
					ok = true
					self = true
				}
			}
			if !ok && s.fix {
				ok = true
				thisUser = "unknown"
			}
			if !ok && !unknownUser {
				// We have a key but no user with that key is known to us.
				// This means an access change has removed permissions for some user
				// but if that user still has the reference, the user could read the file.
				// Someone should run "upspin share -fix" soon to repair the packing.
				unknownUser = true
				fmt.Fprintf(os.Stderr, "%q: cannot find user for key(s); rerun with -fix\n", entry.Name)
				s.state.ExitCode = 1
				continue
			}
		default:
			fmt.Fprintf(os.Stderr, "%q: unrecognized packing %s", entry.Name, packer)
			continue
		}
		if keyUsers != "" {
			keyUsers += " "
		}
		keyUsers += string(thisUser)
	}
	return users, keyUsers, self, nil
}

func userListToString(userList []upspin.UserName) string {
	if userList == nil {
		return "<nil>"
	}
	userString := fmt.Sprint(userList)
	return userString[1 : len(userString)-1]
}

// allEntries expands the arguments to find all the DirEntries identifying items to examine.
// The returned slice contains no directories and no links, only plain files.
func (s *Sharer) allEntries(names []upspin.PathName) []*upspin.DirEntry {
	var entries []*upspin.DirEntry
	// We will not follow links past this point; don't use Client.
	// Use the directory server directly.
	// Glob has processed the higher-level links to get us here.
	for _, name := range names {
		entry, err := s.state.DirServer(name).Lookup(name)
		if err != nil {
			s.state.Exitf("lookup %q: %s", name, err)
		}
		if !entry.IsDir() && !entry.IsLink() {
			entries = append(entries, entry)
			continue
		}
		if entry.IsLink() {
			continue
		}
		if !s.isDir {
			s.state.Exitf("%q is a directory; use -r or -d", name)
		}
		if entry.IsDir() || lookupPacker(entry) != nil {
			// Only work on entries we can pack. Those we can't will be logged.
			entries = append(entries, s.entriesFromDirectory(entry.Name)...)
		}
	}
	return entries
}

// entriesFromDirectory returns the list of all entries in the directory, recursively if required.
func (s *Sharer) entriesFromDirectory(dir upspin.PathName) []*upspin.DirEntry {
	// Get list of files for this directory. See comment in allEntries about links.
	thisDir, err := s.state.DirServer(dir).Glob(upspin.AllFilesGlob(dir))
	if err != nil {
		s.state.Exitf("globbing %q: %s", dir, err)
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
	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		fmt.Fprintf(os.Stderr, "%q has no registered packer for %d; ignoring\n", entry.Name, entry.Packing)
	}
	return packer
}

// addAccess loads an access file.
func (s *Sharer) addAccess(entry *upspin.DirEntry) {
	name := entry.Name
	if !entry.IsDir() {
		name = path.DropPath(name, 1) // Directory name for this file.
	}
	if _, ok := s.accessFiles[name]; ok {
		return
	}
	which, err := s.state.DirServer(name).WhichAccess(entry.Name) // Guaranteed to have no links.
	if err != nil {
		s.state.Exitf("looking up access file %q: %s", name, err)
	}
	var a *access.Access
	if which == nil {
		a, err = access.New(name)
	} else {
		a, err = access.Parse(which.Name, s.state.readOrExit(s.state.Client, which.Name))
	}
	if err != nil {
		s.state.Exitf("parsing access file %q: %s", name, err)
	}
	s.accessFiles[name] = a
	s.users[name] = s.state.usersWithAccess(s.state.Client, a, access.Read)
}

// usersWithReadAccess returns the list of user names granted access by this access file.
func (s *State) usersWithAccess(client upspin.Client, a *access.Access, right access.Right) []upspin.UserName {
	if a == nil {
		return nil
	}
	userList, err := a.Users(right, client.Get)
	if err != nil {
		s.Exitf("getting user list: %s", err)
	}
	return userList
}

// readOrExit returns the contents of the file. It exits if the file cannot be read.
func (s *State) readOrExit(c upspin.Client, file upspin.PathName) []byte {
	data, err := read(c, file)
	if err != nil {
		s.Exitf("%q: %s", file, err)
	}
	return data
}

// read returns the contents of the file.
func read(c upspin.Client, file upspin.PathName) ([]byte, error) {
	fd, err := c.Open(file)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	data, err := ioutil.ReadAll(fd)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// fixShare updates the packdata of the named file to contain wrapped keys for all the users.
func (s *Sharer) fixShare(name upspin.PathName, users []upspin.UserName) {
	directory := s.state.DirServer(name)
	entry, err := directory.Lookup(name) // Guaranteed to have no links.
	if err != nil {
		fmt.Fprintf(os.Stderr, "looking up %q: %s", name, err)
		s.state.ExitCode = 1
		return
	}
	if entry.IsDir() {
		s.state.Exitf("internal error: fixShare called on directory %q", name)
	}
	packer := lookupPacker(entry) // Won't be nil.
	switch packer.Packing() {
	case upspin.EEPack:
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
		// If user is "all", we need to decrypt the file to make progress. We know the file is encrypted.
		if user == access.AllUsers {
			if !s.unencryptForAll {
				fmt.Fprintf(os.Stderr, "Encrypted file %q has read:all access; cannot add keys.\n", name)
				fmt.Fprintf(os.Stderr, "To fix for everyone, rerun with -unencryptforall flag.\n")
				s.state.ExitCode = 1
				return
			}
			s.decrypt(entry)
			return
		}
		// Erroneous or wildcard users will have empty keys here, and be ignored.
		if k := s.lookupKey(user); len(k) > 0 {
			// TODO: Make this general. This works now only because we are always using ee.
			keys = append(keys, k)
			continue
		}
		fmt.Fprintf(os.Stderr, "%q: user %q has no key for packing %s\n", entry.Name, user, packer)
		s.state.ExitCode = 1
		return
	}
	packdatas := []*[]byte{&entry.Packdata}
	packer.Share(s.state.Config, keys, packdatas)
	if packdatas[0] == nil {
		fmt.Fprintf(os.Stderr, "packing skipped for %q\n", entry.Name)
		s.state.ExitCode = 1
		return
	}
	_, err = directory.Put(entry)
	if err != nil {
		// TODO: implement links.
		fmt.Fprintf(os.Stderr, "error putting entry back for %q: %s\n", name, err)
		s.state.ExitCode = 1
	}
}

// decrypt replaces the contents of the encrypted file with cleartext in EEIntegrityPack.
func (s *Sharer) decrypt(entry *upspin.DirEntry) {
	data, err := s.state.Client.Get(entry.Name)
	if err != nil {
		s.state.Failf("reading clear text: %v", err)
		return
	}
	// We have the entry, with an encryption packing, and the clear text.
	// Fix the packing and overwrite.
	entry.Blocks = nil // Lose the old blocks.
	entry.Packing = upspin.EEIntegrityPack
	_, err = s.state.Client.Put(entry.Name, data)
	if err != nil {
		s.state.Failf("writing back decrypted text: %v", err)
		return
	}
}

// lookupKey returns the public key for the user.
// If the user does not exist, is the "all" user, or is a wildcard
// (*@example.com), it returns the empty string.
func (s *Sharer) lookupKey(user upspin.UserName) upspin.PublicKey {
	key, ok := s.userKeys[user] // We use an empty (zero-valued) key to cache failed lookups.
	if ok {
		return key
	}
	if user == access.AllUsers {
		s.userKeys[user] = "<all>"
		return ""
	}
	if isWildcardUser(user) {
		s.userKeys[user] = ""
		return ""
	}
	u, err := s.state.KeyServer().Lookup(user)
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't find key for %q: %s\n", user, err)
		s.state.ExitCode = 1
		s.userKeys[user] = ""
		return ""
	}
	// Remember the lookup, failed or otherwise.
	key = u.PublicKey
	if len(key) == 0 {
		fmt.Fprintf(os.Stderr, "no key for %q\n", user)
		s.state.ExitCode = 1
		s.userKeys[user] = ""
		return ""
	}

	s.userKeys[user] = key
	s.userByHash[sha256.Sum256([]byte(key))] = user
	return key
}

func isWildcardUser(user upspin.UserName) bool {
	return strings.HasPrefix(string(user), "*@")
}
