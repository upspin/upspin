// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package access parses Access and Group files.
//
// If a '#' character is present in a Group or Access file
// the remainder of that line is ignored.
//
// Each line of an Access file specifies a set of rights
// and the users and/or groups to be granted those rights:
// 	<right>[, <right>]: <user/group>[, <user/group>, ...]
// Example:
//	Read,List: user@domain,com, friends
//	Write: user@domain.com, joe@domain.com
//	Delete: user@domain.com # This is a comment.
//
// Each line of a Group file specifies a user or group
// to be included in the group:
// 	<user/group>
// Example:
//	anne@domain.com # A user.
// 	joe@domain.com
// 	admins # A group defined in this user's tree.
//
package access // import "upspin.io/access"

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

const (
	// AccessFile is the base name of an access control file.
	AccessFile = "Access"

	// GroupDir is the base name of the directory of group files in the user root.
	GroupDir = "Group"
)

const (
	// All is a shorthand for AllUsers. Its appearance in a user list
	// grants access to everyone who can authenticate to the Upspin system.
	// This constant can be used in Access files, but will always be expanded
	// to the the full name ("all@upspin.io") when returned from Access.Users
	// and such.
	// If it is present with the Read or "*" rights, it must be the only read write
	// explicitly granted. (Another user can have "*" rights.)
	// All is not allowed to be present in Group files.
	All = "all" // Case is ignored, so "All", "ALL", etc. also work.

	// AllUsers is a reserved Upspin name and is not valid in the text of an
	// Access file. It is the user name that is substituted for the
	// shorthand "all" in a user list. See the comment about All for more
	// details. Its appearance in a user list grants access to everyone who
	// can authenticate to the Upspin system.
	AllUsers upspin.UserName = "all@upspin.io"
)

var (
	allBytes       = []byte(All)
	allUsersBytes  = []byte(AllUsers)
	allUsersParsed path.Parsed
)

func init() {
	var err error
	allUsersParsed, err = path.Parse(upspin.PathName(AllUsers))
	if err != nil {
		panic(err)
	}
}

// ErrPermissionDenied is a predeclared error reporting that a permission check has failed.
// It is not used in this package but is commonly used in its clients.
var ErrPermissionDenied = errors.E(errors.Permission)

// A Right represents a particular access permission: reading, writing, etc.
type Right int

// All the Rights constants.
const (
	Invalid Right = iota - 1
	Read
	Write
	List
	Create
	Delete
	numRights
	AllRights // The superset of rights, written as '*'.
	AnyRight  // All users holding any right, used from WhichAccess.
)

// rightNames are the names of the rights, in order (and missing invalid).
var rightNames = [][]byte{
	[]byte("read"),
	[]byte("write"),
	[]byte("list"),
	[]byte("create"),
	[]byte("delete"),
}

// String returns a textual representation of the right.
func (r Right) String() string {
	if r == AnyRight {
		return "any"
	}
	if r < 0 || numRights <= r {
		return "invalidRight"
	}
	return string(rightNames[r])
}

var (
	// mu controls access to the groups map
	mu sync.RWMutex

	// groups holds the parsed list of all known groups,
	// indexed by group name (joe@blow.com/Group/nerds).
	// It is global so multiple Access files can share
	// group definitions.
	groups = make(map[upspin.PathName][]path.Parsed)
)

// Access represents a parsed Access file.
type Access struct {
	// path is parsed path name of the file.
	parsed path.Parsed

	// owner is the user@domain.com name of the path of the file.
	owner upspin.UserName

	// domain is the domain.com part of the user name of the path of the file.
	domain string

	// worldReadable states whether the file is world-readable, that is, has read:all
	worldReadable bool

	// list holds the lists of parsed user and group names.
	// It is indexed by a right.
	// Each list is stored in sorted order.
	list [numRights][]path.Parsed

	// All the lists are concatenated into this single slice, for easy evaluation of the
	// "Any" right. That is, the lists above are all subslices of this list.
	// Note that this list will be neither sorted nor deduplicated.
	allUsers []path.Parsed
}

// Path returns the full path name of the file that was parsed.
func (a *Access) Path() upspin.PathName {
	return a.parsed.Path()
}

// Parse parses the contents of the path name, in data, and returns the parsed Access.
func Parse(pathName upspin.PathName, data []byte) (*Access, error) {
	const op = "access.Parse"
	a, parsed, err := newAccess(pathName)
	if err != nil {
		return nil, err
	}
	// Temporaries. Pre-allocate so they can be reused in the loop, saving allocations.
	rights := make([][]byte, 10)
	users := make([][]byte, 10)
	s := bufio.NewScanner(bytes.NewReader(data))
	numReaders := 0
	var userAll []byte
	for lineNum := 1; s.Scan(); lineNum++ {
		line := clean(s.Bytes())
		if len(line) == 0 {
			continue
		}

		// A line is two non-empty comma-separated lists, separated by a colon.
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			return nil, errors.E(op, pathName, errors.Invalid, errors.Errorf("no colon on line %d: %q", lineNum, line))
		}

		// Parse rights and users lists.
		rightsText := bytes.TrimSpace(line[:colon]) // TrimSpace for good error messages below.
		rights = splitList(rights[:0], rightsText)
		if rights == nil {
			return nil, errors.E(op, pathName, errors.Invalid, errors.Errorf("invalid rights list on line %d: %q", lineNum, rightsText))
		}
		usersText := bytes.TrimSpace(line[colon+1:])
		users = splitList(users[:0], usersText)
		if users == nil {
			return nil, errors.E(op, pathName, errors.Invalid, errors.Errorf("invalid users list on line %d: %q", lineNum, usersText))
		}

		var err error
		var all []byte
		for _, right := range rights {
			switch r := which(right); r {
			case AllRights:
				for r := Right(0); r < numRights; r++ {
					all, err = a.addRight(r, parsed.User(), users)
					if all != nil && r == Read {
						a.worldReadable = true
						userAll = append([]byte(nil), all...)
						numReaders++ // We count all as a reader if granted "*" rights.
					}
				}
			case Read:
				all, err = a.addRight(r, parsed.User(), users)
				if all != nil {
					a.worldReadable = true
					userAll = append([]byte(nil), all...)
					numReaders += len(users)
				}
			case Write, List, Create, Delete:
				_, err = a.addRight(r, parsed.User(), users)
			case Invalid:
				err = errors.Errorf("invalid access rights on line %d: %q", lineNum, right)
			}
			if err != nil {
				return nil, errors.E(op, pathName, errors.Invalid, err)
			}
		}
	}
	if s.Err() != nil {
		return nil, s.Err()
	}
	// How many users in all? Allocate the a.allUsers list in one go.
	numUsers := 0
	for _, r := range a.list {
		numUsers += len(r)
	}
	a.allUsers = make([]path.Parsed, 0, numUsers)
	// Sort the lists, then repack them into the "all" users list.
	// It does not remove duplicates.
	for i, r := range a.list {
		sort.Sort(sliceOfParsed(r))
		a.list[i] = a.allUsers[len(a.allUsers) : len(a.allUsers)+len(r)]
		a.allUsers = append(a.allUsers, r...)
	}
	if numReaders > 1 && a.worldReadable {
		return nil, errors.E(op, pathName, errors.Invalid, errors.Errorf("%q cannot appear with other users", userAll))
	}
	return a, nil
}

func (a *Access) addRight(r Right, owner upspin.UserName, users [][]byte) ([]byte, error) {
	// Save allocations by doing some pre-emptively.
	if a.list[r] == nil {
		a.list[r] = make([]path.Parsed, 0, preallocSize(len(users)))
	}
	var err error
	var all []byte
	a.list[r], all, err = parsedAppend(a.list[r], owner, users...)
	return all, err
}

// New returns a new Access granting the owner of pathName all rights.
// It represents rights equivalent to the those granted to the owner if no Access
// files are present in the owner's tree.
func New(pathName upspin.PathName) (*Access, error) {
	a, parsed, err := newAccess(pathName)
	if err != nil {
		return nil, err
	}
	// We're being clever here and not parsing a new path just to get the user name from it.
	// Just re-use the same one with just the user portion of it set.
	userPath := parsed.First(0)

	list := []path.Parsed{userPath}
	for i := range a.list {
		a.list[i] = list
	}
	return a, nil
}

func newAccess(pathName upspin.PathName) (*Access, *path.Parsed, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, nil, err
	}
	_, _, domain, err := user.Parse(parsed.User())
	// We don't expect an error since it's been parsed, but check anyway.
	if err != nil {
		return nil, nil, err
	}
	a := &Access{
		parsed: parsed,
		owner:  parsed.User(),
		domain: domain,
	}
	return a, &parsed, nil
}

// For sorting the lists of paths.
type sliceOfParsed []path.Parsed

func (s sliceOfParsed) Len() int           { return len(s) }
func (s sliceOfParsed) Less(i, j int) bool { return s[i].Compare(s[j]) < 0 }
func (s sliceOfParsed) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func isSpace(b byte) bool {
	switch b {
	case ' ', '\r', '\f', '\v', '\n', '\t':
		return true
	default:
		return false
	}
}

func isSeparator(b byte) bool {
	return b == ',' || isSpace(b)
}

// clean takes a line of text and removes comments and starting and leading space.
// It returns an empty slice if nothing is left.
func clean(line []byte) []byte {
	// Remove comments.
	if index := bytes.IndexByte(line, '#'); index >= 0 {
		line = line[:index]
	}

	// Ignore blank lines.
	return bytes.TrimSpace(line)
}

// isAll is a case-insensitive check for "all".
func isAll(user []byte) bool {
	// Check for length to be fast. Safe because "all" is ASCII.
	return len(user) == len(allBytes) && bytes.EqualFold(user, allBytes)
}

// isAllUsers is a case-insensitive check "all@upspin.io".
func isAllUsers(user []byte) bool {
	// Check for length to be fast. Safe because "all@upspin.io" is ASCII.
	return len(user) == len(allUsersBytes) && bytes.EqualFold(user, allUsersBytes)
}

// parsedAppend parses the users (as path.Parse values) and appends them to the list.
// The returned byte slice is empty unless "all" is present, in which case the text of
// the provided user name is returned, for use in error messages.
// The check is case-insensitive.
func parsedAppend(list []path.Parsed, owner upspin.UserName, users ...[]byte) ([]path.Parsed, []byte, error) {
	var all []byte
	for _, user := range users {
		// Reject "all@upspin.io" as user input.
		if isAllUsers(user) {
			return nil, nil, errors.Errorf("reserved user name %q", user)
		}
		// Case-insensitive check for "all" which we canonicalize to "all@upspin.io".
		// We require it to be the only item on the line.
		if isAll(user) {
			all = user
			user = allUsersBytes
		}
		p, err := path.Parse(upspin.PathName(user) + "/")
		if err != nil {
			if bytes.IndexByte(user, '@') >= 0 {
				// Has user name but doesn't parse: Invalid path.
				return nil, nil, err
			}
			// Is it a badly formed group file?
			const groupElem = "/" + GroupDir + "/"
			slash := bytes.IndexByte(user, '/')
			if slash >= 0 && bytes.Index(user, []byte(groupElem)) == slash {
				// Looks like a group file but is missing the user name.
				return nil, nil, errors.Errorf("bad user name in group path %q", user)
			}
			// It has no user name, so it might be a path name for a group file.
			p, err = path.Parse(upspin.PathName(owner) + groupElem + upspin.PathName(user))
			if err != nil {
				return nil, nil, err
			}
			if err := isValidGroup(p); err != nil {
				return nil, nil, err
			}
		}
		// Check group syntax.
		if !p.IsRoot() {
			if err := isValidGroup(p); err != nil {
				return nil, nil, err
			}
		}
		list = append(list, p)
	}
	return list, all, nil
}

func isValidGroup(p path.Parsed) error {
	// First element must be group.
	if p.Elem(0) != GroupDir {
		return errors.Errorf("illegal group %q", p)
	}
	// Groups cannot be wild cards.
	if strings.HasPrefix(p.String(), "*@") {
		return errors.Errorf("cannot have wildcard for group name %q", p)
	}
	// All name elements must be well-behaved to avoid parsing problems.
	for i := 1; i < p.NElem(); i++ { // Element 0 is "Group".
		if _, _, err := user.ParseUser(p.Elem(i)); err != nil {
			return err
		}
	}
	return nil
}

// splitList parses a comma- or space-separated list, skipping other
// white space. It returns nil
// if the list is badly formed. We avoid bytes.Split because it allocates.
func splitList(list [][]byte, text []byte) [][]byte {
	// One comma-, space- or EOF-terminated element per iteration.
	for i, j := 0, 0; i < len(text); i = j {
		for j = i; j != len(text) && !isSeparator(text[j]); j++ {
		}
		list = append(list, text[i:j])
		// Skip separators, but allow only one comma.
		for sawComma := false; j < len(text) && isSeparator(text[j]); j++ {
			if text[j] == ',' {
				if sawComma {
					return nil
				}
				sawComma = true
			}
		}
	}
	if len(list) == 0 {
		return nil
	}
	for i, elem := range list {
		elem = bytes.TrimSpace(elem)
		if !isPlausibleUserOrGroupName(elem) {
			return nil
		}
		list[i] = elem
	}
	return list
}

// isPlausibleUserOrGroupName reports whether the name is sane enough to
// possibly be a user name or a group name. Group names can be just a single
// path element, so lacking any better definition we force it to be printable,
// and also free of space, comma, or colon to avoid parsing ambiguities, just
// to be safe. The argument is a byte slice, not a string, which keeps us away
// from the string-based valid and user packages, which could do a better
// job, but they are invoked higher up once we have a string. This function
// is mostly about validating the syntax of Access and Group files.
func isPlausibleUserOrGroupName(name []byte) bool {
	if len(name) == 0 {
		return false
	}
	// Need to UTF-8 decode by hand, as name is []byte not string. Don't allocate.
	for i, width := 0, 0; i < len(name); i += width {
		var r rune
		r, width = utf8.DecodeRune(name[i:])
		switch {
		case r == ':':
			return false // Bad syntax: spurious colon in user list.
		case width == 1 && isSeparator(byte(r)):
			return false // More bad syntax. Shouldn't happen but be careful.
		case width == 1 && r == utf8.RuneError:
			return false // Bad UTF-8.
		case !strconv.IsPrint(r):
			return false // Bad character for name.
		}
	}
	return true
}

// toLower lower cases a single character.
func toLower(b byte) byte {
	// An old trick: In ASCII the characters line up bitwise so this changes any letter to lower case.
	return b | ('a' - 'A')
}

// which reports which right the text represents. Case is ignored and a right may be
// specified by its first letter only. We know that the text is not empty.
func which(right []byte) Right {
	if bytes.Equal(right, []byte{'*'}) {
		return AllRights
	}
	for i, c := range right {
		right[i] = toLower(c)
	}
	for r, name := range rightNames {
		// Match either a single letter or the exact name.
		if len(right) == 1 && right[0] == name[0] || bytes.Equal(right, name) {
			return Right(r)
		}
	}
	return Invalid
}

// IsAccessFile reports whether the pathName ends in a file named Access, which is special.
func IsAccessFile(pathName upspin.PathName) bool {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return false
	}
	// Must end "/Access".
	return parsed.NElem() >= 1 && parsed.Elem(parsed.NElem()-1) == AccessFile
}

// IsGroupFile reports whether the pathName contains a directory in the root named Group, which is special.
func IsGroupFile(pathName upspin.PathName) bool {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return false
	}
	// Need "a@b.c/Group/file", but file can't be Access.
	return parsed.NElem() >= 2 && parsed.Elem(0) == GroupDir && parsed.Elem(parsed.NElem()-1) != AccessFile
}

// IsAccessControlFile reports whether the pathName represents a file used for
// access control. At the moment that means either an Access or a Group file.
func IsAccessControlFile(pathName upspin.PathName) bool {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return false
	}
	nElem := parsed.NElem()
	// To be an Access file, must end "/Access".
	if nElem >= 1 && parsed.Elem(nElem-1) == AccessFile {
		return true
	}
	// To be a Group file, need "a@b.c/Group/file". Don't worry about Access file; that's already done.
	if nElem >= 2 && parsed.Elem(0) == GroupDir {
		return true
	}
	return false
}

// AddGroup installs a group with the specified name and textual contents,
// which should have been read from the group file with that name.
// If the group is already known, its definition is replaced.
func AddGroup(pathName upspin.PathName, contents []byte) error {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return err
	}
	group, err := ParseGroup(parsed, contents)
	if err != nil {
		return err
	}
	mu.Lock()
	groups[parsed.Path()] = group
	mu.Unlock()
	return nil
}

// RemoveGroup undoes the installation of a group added by AddGroup.
// It returns an error if the path is bad or the group is not present.
func RemoveGroup(pathName upspin.PathName) error {
	const op = "access.RemoveGroup"
	parsed, err := path.Parse(pathName)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if _, found := groups[parsed.Path()]; !found {
		return errors.E(op, errors.NotExist, errors.Str("group does not exist"))
	}
	delete(groups, parsed.Path())
	return nil
}

// ParseGroup parses a group file but does not call AddGroup to install it.
func ParseGroup(parsed path.Parsed, contents []byte) (group []path.Parsed, err error) {
	const op = "access.ParseGroup"
	// Temporary. Pre-allocate so it can be reused in the loop, saving allocations.
	users := make([][]byte, 10)
	s := bufio.NewScanner(bytes.NewReader(contents))
	for lineNum := 1; s.Scan(); lineNum++ {
		line := clean(s.Bytes())
		if len(line) == 0 {
			continue
		}

		users = splitList(users[:0], line)
		if users == nil {
			return nil, errors.E(op, parsed.Path(), errors.Invalid,
				errors.Errorf("syntax error in group file on line %d", lineNum))
		}
		if group == nil {
			group = make([]path.Parsed, 0, preallocSize(len(users)))
		}
		var all []byte
		group, all, err = parsedAppend(group, parsed.User(), users...)
		if all != nil {
			return nil, errors.E(op, parsed.Path(), errors.Invalid,
				errors.Errorf("cannot use user %q in group file on line %d", all, lineNum))
		}
		if err != nil {
			return nil, errors.E(op, parsed.Path(), errors.Invalid,
				errors.Errorf("bad group users list on line %d: %v", lineNum, err))
		}
	}
	if s.Err() != nil {
		return nil, errors.E(op, errors.IO, s.Err())
	}
	return group, nil
}

// preallocSize returns a sensible preallocation size for a list that will contain
// at least n users, providing a little headroom.
func preallocSize(n int) int {
	switch {
	case n > 100:
		return n + 20
	case n > 10:
		return 2 * n
	default:
		return 16
	}
}

// canNoGroupLoad reports whether the requesting user can access the file
// using the specified right according to the rules of the Access
// file. If it needs to read some group files before the decision can be made,
// it returns a non-nil slice of their names.
func (a *Access) canNoGroupLoad(requester upspin.UserName, right Right, pathName upspin.PathName) (bool, []upspin.PathName, error) {
	parsedRequester, err := path.Parse(upspin.PathName(requester + "/"))
	if err != nil {
		return false, nil, err
	}
	isOwner := requester == a.owner
	// If user is the owner and the request is for read, list, or any access, access is granted.
	if isOwner {
		switch right {
		case Read, List, AnyRight:
			// Owner can always read or list anything in the owner's tree.
			return true, nil, nil
		}
	}
	// If the file is an Access or Group file, the owner has full rights always; no one else
	// can write it.
	if IsAccessControlFile(pathName) {
		switch right {
		case Write, Create, Delete:
			return isOwner, nil, nil
		}
	}
	list, err := a.getListFor(right)
	if err != nil {
		return false, nil, err
	}
	// First try the list of regular users we have loaded. Make a note of groups to check.
	found, groupsToCheck := a.inList(parsedRequester, list, nil)
	if found {
		return true, nil, nil
	}
	// Now look at the groups we have to check, and build a list of
	// groups we don't know about in case we can't answer with
	// what we know so far.
	var missingGroups []upspin.PathName
	// This is not a range loop because the groups list may grow.
	for i := 0; i < len(groupsToCheck); i++ {
		group := groupsToCheck[i]
		groupPath := group.Path()
		mu.RLock()
		known, ok := groups[groupPath]
		mu.RUnlock()
		if !ok {
			missingGroups = append(missingGroups, groupPath)
			continue
		}
		// The owner of a group is automatically a member of the group
		if group.User() == requester {
			return true, nil, nil
		}
		found, groupsToCheck = a.inList(parsedRequester, known, groupsToCheck)
		if found {
			return true, nil, nil
		}
	}
	return false, missingGroups, nil
}

// Can reports whether the requesting user can access the file
// using the specified right according to the rules of the Access
// file. It also interprets the rules that the owner can always
// Read and List, and only the owner can create or modify
// Access and Group files.
//
// The rights are applied to the path itself. For instance, for Create
// the question is whether the user can create the named file, not
// whether the user has Create rights in the directory with that name.
// Similarly, for List the question is whether the user can list the
// status of this file, or if it is a directory, list the contents
// of that directory. It is the caller's responsibility to apply the
// correct Access file to the question, and separately to verify
// issues such as attempts to write to a directory rather than a file.
//
// The method loads Group files as needed by
// calling the provided function to read each file's contents.
//
// If a Group file cannot be loaded or parsed that failure is
// reported only if the requester does not match any names that
// can be found in the Access file or other Group files.
func (a *Access) Can(requester upspin.UserName, right Right, pathName upspin.PathName, load func(upspin.PathName) ([]byte, error)) (bool, error) {
	var groupErr error
	var failedGroups []upspin.PathName
	for {
		granted, missing, err := a.canNoGroupLoad(requester, right, pathName)
		if err != nil {
			return false, err
		}
		if missing == nil {
			return granted, nil
		}

		loaded := false
	missingLoop:
		for _, group := range missing {
			// Don't load this group if it failed
			// in a previous iteration.
			for _, g := range failedGroups {
				if g == group {
					continue missingLoop
				}
			}

			// Load and parse the group.
			data, err := load(group)
			if err == nil {
				err = AddGroup(group, data)
				if err == nil {
					loaded = true
					continue
				}
			}

			// Remember failures.
			failedGroups = append(failedGroups, group)
			if groupErr != nil {
				groupErr = err
			}
		}
		// We have tried and failed to retrieve all of missing. Give up.
		if !loaded {
			break
		}
	}
	return false, groupErr
}

// expandGroups expands a list of groups to the user names they represent.
// If the Access file does not know the members of a group that it
// needs to resolve the answer, it returns a list of the group files it needs to have read for it.
// The caller should fetch these and report them with the AddGroup method, then retry.
// TODO: use this in Can.
func (a *Access) expandGroups(toExpand []upspin.PathName) (userNames []upspin.UserName, missingGroups []upspin.PathName) {
	for i := 0; i < len(toExpand); i++ { // not range since list may grow
		group := toExpand[i]
		mu.RLock()
		usersFromGroup, found := groups[group]
		mu.RUnlock()
		if found {
			for _, p := range usersFromGroup {
				if p.IsRoot() {
					// Dups are okay here, unique sorting is done by caller when no more missingGroups are found.
					userNames = append(userNames, p.User())
				} else {
					// This means there are nested Groups.
					// Add it to the list to expand if not already there.
					toExpand = appendUnique(toExpand, p.Path())
				}
			}
		} else {
			// Add to missingGroups if not already there.
			// (This could be done via a sorted list but is not.  Expect the list to remain short.)
			missingGroups = appendUnique(missingGroups, group)
		}
	}
	return
}

// appendUnique returns slice, with elem appended unless elem is already a member.
func appendUnique(slice []upspin.PathName, elem upspin.PathName) []upspin.PathName {
	if contains(slice, elem) {
		return slice
	}
	return append(slice, elem)
}

func contains(haystack []upspin.PathName, needle upspin.PathName) bool {
	for _, hay := range haystack {
		if hay == needle {
			return true
		}
	}
	return false
}

func (a *Access) getListFor(right Right) ([]path.Parsed, error) {
	switch right {
	case Read, Write, List, Create, Delete:
		return a.list[right], nil
	case AnyRight:
		return a.allUsers, nil
	default:
		return nil, errors.Errorf("unrecognized right value %d", right)
	}
}

// usersNoGroupLoad returns the user names granted a given right according to the rules
// of the Access file. It also interprets the rule that the owner can always
// Read and List. The list is unsorted and may contain duplicates.
//
// If the Access file does not know the members of a group that it
// needs to resolve the answer, it returns a list of the group files it needs to
// have read for it. The caller should fetch these and report them
// with the AddGroup method, then retry.
func (a *Access) usersNoGroupLoad(right Right) ([]upspin.UserName, []upspin.PathName, error) {
	list, err := a.getListFor(right)
	if err != nil {
		return nil, nil, err
	}
	userNames := make([]upspin.UserName, 0, len(list)+1)
	switch right {
	case Read, List:
		userNames = append(userNames, a.owner)
	}
	var groups []upspin.PathName
	for _, user := range list {
		if user.IsRoot() {
			// It's a user
			userNames = append(userNames, user.User())
		} else {
			// It's a group. Need to unroll groups.
			groups = append(groups, user.Path())
		}
	}
	if len(groups) > 0 {
		users, missingGroups := a.expandGroups(groups)
		userNames = append(userNames, users...)
		if missingGroups != nil {
			return userNames, missingGroups, err
		}
	}
	return userNames, nil, nil
}

// For sorting the lists of UserNames.
type sliceOfUserName []upspin.UserName

func (s sliceOfUserName) Len() int           { return len(s) }
func (s sliceOfUserName) Less(i, j int) bool { return s[i] < s[j] }
func (s sliceOfUserName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// List returns the list of users and groups granted the specified right. Unlike
// the Users method, List returns the original unexpanded members from the Access
// file. In particular, groups appear as their original group names rather than as
// the users they represent. The returned values are parsed path names. If they are
// roots, they represent users; otherwise they represent groups. List is useful
// mainly for diagnosing permission problems; the Users method has more quotidian
// uses.
func (a *Access) List(right Right) []path.Parsed {
	// Make a copy to avoid the caller modifying the Access struct.
	var list []path.Parsed
	if right == AnyRight {
		list = a.allUsers
	} else {
		list = a.list[right]
	}
	if list == nil {
		return nil
	}
	out := make([]path.Parsed, len(list))
	copy(out, list)
	return out
}

// Users returns the user names granted a given right according to the rules
// of the Access file. It also interprets the rule that the owner can always
// Read and List.  Users loads group files as needed by calling the provided
// function to read each file's contents.
func (a *Access) Users(right Right, load func(upspin.PathName) ([]byte, error)) ([]upspin.UserName, error) {
	var userNames []upspin.UserName
	for {
		users, neededGroups, err := a.usersNoGroupLoad(right)
		if err != nil {
			return nil, err
		}
		if neededGroups == nil {
			userNames = users
			break
		}
		for _, group := range neededGroups {
			groupData, err := load(group)
			if err != nil {
				return nil, err
			}
			err = AddGroup(group, groupData)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(userNames) == 0 {
		return nil, nil
	}
	sort.Sort(sliceOfUserName(userNames))
	// Remove duplicates.
	for i := 0; i < len(userNames)-1; {
		if userNames[i] == userNames[i+1] {
			userNames = append(userNames[:i], userNames[i+1:]...)
			continue
		}
		i++
	}

	return userNames, nil
}

// MarshalJSON returns a JSON-encoded representation of this Access struct.
func (a *Access) MarshalJSON() ([]byte, error) {
	const op = "access.MarshalJSON"
	// We need to export a field of Access but we don't want to make it public,
	// so we encode it separately.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(a.list); err != nil {
		return nil, errors.E(op, err)
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON returns an Access given its path name and its JSON encoding.
func UnmarshalJSON(name upspin.PathName, jsonAccess []byte) (*Access, error) {
	const op = "access.UnmarshalJSON"
	var list [numRights][]path.Parsed
	err := json.Unmarshal(jsonAccess, &list)
	if err != nil {
		return nil, errors.E(op, err)
	}
	access := &Access{
		list: list,
	}
	access.parsed, err = path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	access.owner = access.parsed.User()
	_, _, access.domain, err = user.Parse(access.parsed.User())
	if err != nil {
		return nil, errors.E(op, err)
	}
	return access, nil
}

// inList reports whether the requester is present in the list, either directly or by wildcard.
// If we encounter a new group in the list, we add it the list of groups to check and will
// process it in another call from Can.
func (a *Access) inList(requester path.Parsed, list []path.Parsed, groupsToCheck []path.Parsed) (bool, []path.Parsed) {
	_, _, domain, err := user.Parse(requester.User())
	// We don't expect an error since it's been parsed, but check anyway.
	if err != nil {
		return false, nil
	}
Outer:
	for _, allowed := range list {
		// Simple test for AllUsers, granting universal access.
		if allowed == allUsersParsed {
			return true, nil
		}
		if allowed.IsRoot() {
			if allowed.User() == requester.User() {
				return true, nil
			}
			// Wildcard: The path name *@domain.com matches anyone in domain.
			if strings.HasPrefix(string(allowed.User()), "*@") && string(allowed.User()[2:]) == domain {
				return true, nil
			}
		} else {
			// It's a group. Make sure we don't put the same one in twice. TODO: Could be n^2.
			for _, group := range groupsToCheck {
				if allowed.Equal(group) {
					// Already there, so don't add it again.
					continue Outer
				}
			}
			groupsToCheck = append(groupsToCheck, allowed)
		}
	}
	return false, groupsToCheck
}

// IsReadableByAll reports whether the Access file has read:all or read:all@upspin.io
func (a *Access) IsReadableByAll() bool {
	return a.worldReadable
}
