// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package access parses access control files.
//
// Access files have the following format:
// <access type>[, <access type]: <email>[, <email>, ...]
//
// Anything after a '#' character is ignored
//
// Example:
//
//	Read: email@domain,com, email2@domain.com
//	Write: writer@domain.com, writer2@domain.com, writer3@example,com
//	Append,Write: appender@example.com # This is a comment
package access

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	// accessFile is the name of the Access file.
	accessFile = "Access"
)

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
	AllRights // The superset, written as '*'.
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

	// user is the user@domain.com name of the path of the file.
	owner upspin.UserName

	// domain is the domain.com part of the user name of the path of the file.
	domain string

	// list holds the lists of parsed user and group names.
	// It is indexed by a right. Each list is stored in sorted
	// order, mostly so Equal can be efficient.
	list [numRights][]path.Parsed
}

// Path returns the full path name of the file that was parsed.
func (a *Access) Path() upspin.PathName {
	return a.parsed.Path()
}

// Parse parses the contents of the path name, in data, and returns the parsed Access.
func Parse(pathName upspin.PathName, data []byte) (*Access, error) {
	a, parsed, err := newAccess(pathName)
	if err != nil {
		return nil, err
	}
	// Temporaries. Pre-allocate so they can be reused in the loop, saving allocations.
	rights := make([][]byte, 10)
	users := make([][]byte, 10)
	s := bufio.NewScanner(bytes.NewReader(data))
	for lineNum := 1; s.Scan(); lineNum++ {
		line := clean(s.Bytes())
		if len(line) == 0 {
			continue
		}

		// A line is two non-empty comma-separated lists, separated by a colon.
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			return nil, errors.Errorf("%s:%d: syntax error: no colon on line: %q", pathName, lineNum, line)
		}

		// Parse rights and users lists.
		rightsText := bytes.TrimSpace(line[:colon]) // TrimSpace for good error messages below.
		rights = splitList(rights[:0], rightsText)
		if rights == nil {
			return nil, errors.Errorf("%s:%d: syntax error: invalid rights list: %q", pathName, lineNum, rightsText)
		}
		usersText := bytes.TrimSpace(line[colon+1:])
		users := splitList(users[:0], usersText)
		if users == nil {
			return nil, errors.Errorf("%s:%d: syntax error: invalid users list: %q", pathName, lineNum, usersText)
		}

		var err error
		for _, right := range rights {
			switch r := which(right); r {
			case AllRights:
				for r := Right(0); r < numRights; r++ {
					err = a.addRight(r, parsed.User(), users)
				}
			case Read, Write, List, Create, Delete:
				err = a.addRight(r, parsed.User(), users)
			case Invalid:
				err = errors.Errorf("%s:%d: invalid right: %q", pathName, lineNum, right)
			}
			if err != nil {
				return nil, errors.Errorf("%s:%d: bad users list %q: %v", pathName, lineNum, usersText, err)
			}
		}
	}
	if s.Err() != nil {
		return nil, s.Err()
	}
	// Sort the lists.
	for _, r := range a.list {
		sort.Sort(sliceOfParsed(r))
	}
	return a, nil
}

func (a *Access) addRight(r Right, owner upspin.UserName, users [][]byte) error {
	// Save allocations by doing some pre-emptively.
	if a.list[r] == nil {
		a.list[r] = make([]path.Parsed, 0, preallocSize(len(users)))
	}
	var err error
	a.list[r], err = parsedAppend(a.list[r], owner, users...)
	return err
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
	_, domain, err := path.UserAndDomain(parsed.User())
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

// Equal reports whether a and b have equal contents.
func (a *Access) Equal(b *Access) bool {
	if a.parsed.Compare(b.parsed) != 0 {
		return false
	}
	if len(a.list) != len(b.list) {
		return false
	}
	for i, al := range a.list {
		bl := b.list[i]
		if len(al) != len(bl) {
			return false
		}
		for j, ar := range al {
			if ar.Compare(bl[j]) != 0 {
				return false
			}
		}
	}
	return true
}

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
// It returns an emtpy slice if nothing is left.
func clean(line []byte) []byte {
	// Remove comments.
	if index := bytes.IndexByte(line, '#'); index >= 0 {
		line = line[:index]
	}

	// Ignore blank lines.
	return bytes.TrimSpace(line)
}

// parsedAppend parses the users (as path.Parse values) and appends them to the list.
func parsedAppend(list []path.Parsed, owner upspin.UserName, users ...[]byte) ([]path.Parsed, error) {
	for _, user := range users {
		p, err := path.Parse(upspin.PathName(user) + "/")
		if err != nil {
			if bytes.IndexByte(user, '@') >= 0 {
				// Has user name but doesn't parse: Invalid path.
				return nil, err
			}
			// Is it a badly formed group file?
			const groupElem = "/Group/"
			slash := bytes.IndexByte(user, '/')
			if slash >= 0 && bytes.Index(user, []byte(groupElem)) == slash {
				// Looks like a group file but is missing the user name.
				return nil, errors.Errorf("bad user name in group path %q", user)
			}
			// It has no user name, so it might be a path name for a group file.
			p, err = path.Parse(upspin.PathName(owner) + groupElem + upspin.PathName(user))
			if err != nil {
				return nil, err
			}
		}
		// Check group syntax.
		if !p.IsRoot() {
			// First element must be group.
			if p.Elem(0) != "Group" {
				return nil, errors.Errorf("illegal group %q", user)
			}
			// Groups cannot be wild cards.
			if bytes.HasPrefix(user, []byte("*@")) {
				return nil, errors.Errorf("cannot have wildcard for group name %q", user)
			}
		}
		list = append(list, p)
	}
	return list, nil
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
		if !isValidUserOrGroupName(elem) {
			return nil
		}
		list[i] = elem
	}
	return list
}

// TODO: What is the right syntax for a user/group name?
func isValidUserOrGroupName(name []byte) bool {
	if len(name) == 0 {
		return false
	}
	for _, b := range name {
		if isSpace(b) || b == ':' {
			return false
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

// IsAccessFile reports whether the pathName contains a file named Access, which is special.
func IsAccessFile(pathName upspin.PathName) bool {
	return strings.HasSuffix(string(pathName), accessFile)
}

// IsGroupFile reports whether the pathName contains a directory in the root named Group, which is special.
func IsGroupFile(pathName upspin.PathName) bool {
	path := string(pathName)
	slash := strings.IndexByte(path, '/')
	return slash > 0 && slash == strings.Index(path, "/Group/")
}

// AddGroup installs a group with the specified name and textual contents,
// which should have been read from the group file with that name.
// If the group is already known, its definition is replaced.
func AddGroup(pathName upspin.PathName, contents []byte) error {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return err
	}
	group, err := parseGroup(parsed, contents)
	if err != nil {
		return err
	}
	mu.Lock()
	groups[parsed.Path()] = group
	mu.Unlock()
	return nil
}

// RemoveGroup undoes the installation of a group added by AddGroup.
func RemoveGroup(pathName upspin.PathName) error {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if _, found := groups[parsed.Path()]; !found {
		return errors.E("RemoveGroup", errors.NotExist, errors.Str("group does not exist"))
	}
	delete(groups, parsed.Path())
	return nil
}

// parseGroup parses a group file but does not install it in the groups map.
func parseGroup(parsed path.Parsed, contents []byte) (group []path.Parsed, err error) {
	// Temporary. Pre-allocate so it can be reused in the loop, saving allocations.
	users := make([][]byte, 10)
	s := bufio.NewScanner(bytes.NewReader(contents))
	for lineNum := 1; s.Scan(); lineNum++ {
		line := clean(s.Bytes())
		if len(line) == 0 {
			continue
		}

		users := splitList(users[:0], line)
		if users == nil {
			return nil, errors.Errorf("%s:%d: syntax error in group file: %q", parsed, lineNum, line)
		}
		if group == nil {
			group = make([]path.Parsed, 0, preallocSize(len(users)))
		}
		group, err = parsedAppend(group, parsed.User(), users...)
		if err != nil {
			return nil, errors.Errorf("%s:%d: bad group users list %q: %v", parsed, lineNum, line, err)
		}
	}
	if s.Err() != nil {
		return nil, s.Err()
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
	// If user is the owner and the request is for read access, access is granted.
	if isOwner {
		switch right {
		case Read, List:
			// Owner can always read or list anything in the owner's tree.
			return true, nil, nil
		}
	}
	// If the file is an Access or Group file, the owner has full rights always; no one else
	// can write it.
	if IsAccessFile(pathName) || IsGroupFile(pathName) {
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
// file.
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
// The method loads group files as needed by
// calling the provided function to read each file's contents.
func (a *Access) Can(requester upspin.UserName, right Right, pathName upspin.PathName, load func(upspin.PathName) ([]byte, error)) (bool, error) {
	for {
		granted, missing, err := a.canNoGroupLoad(requester, right, pathName)
		if err != nil {
			return false, err
		}
		if missing == nil {
			return granted, nil
		}
		for _, group := range missing {
			data, err := load(group)
			if err != nil {
				return false, err
			}
			err = AddGroup(group, data)
			if err != nil {
				return false, err
			}
		}
	}
}

// expandGroups expands a list of groups to the user names they represent.
// If the Access file does not know the members of a group that it
// needs to resolve the answer, it returns a list of the group files it needs to have read for it.
// The caller should fetch these and report them with the AddGroup method, then retry.
// TODO: use this in Can.
func (a *Access) expandGroups(toExpand []upspin.PathName) ([]upspin.UserName, []upspin.PathName) {
	var missingGroups []upspin.PathName
	var userNames []upspin.UserName
Outer:
	for i := 0; i < len(toExpand); i++ { // not range since list may grow
		group := toExpand[i]
		mu.RLock()
		usersFromGroup, found := groups[group]
		mu.RUnlock()
		if found {
			for _, p := range usersFromGroup {
				if p.IsRoot() {
					userNames = append(userNames, p.User())
				} else {
					// This means there are nested Groups.
					// Add it to the list to expand if not already there.
					newGroupToExpand := p.Path()
					for _, te := range toExpand {
						if te == newGroupToExpand {
							continue Outer
						}
					}
					toExpand = append(toExpand, newGroupToExpand)
				}
			}
		} else {
			// Add to missingGroups if not already there.
			for _, mg := range missingGroups {
				if string(group) == string(mg) {
					continue Outer
				}
			}
			missingGroups = append(missingGroups, group)
		}
	}
	if len(missingGroups) > 0 {
		return userNames, missingGroups
	}
	return userNames, nil
}

func (a *Access) getListFor(right Right) ([]path.Parsed, error) {
	switch right {
	case Read, Write, List, Create, Delete:
		return a.list[right], nil
	default:
		return nil, errors.Errorf("unrecognized right value %d", right)
	}
}

// usersNoGroupLoad returns the user names granted a given right according to the rules
// of the Access file.
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
	userNames := make([]upspin.UserName, 0, len(list))
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
		userNames = mergeUsers(userNames, users)
		if missingGroups != nil {
			return userNames, missingGroups, err
		}
	}
	return userNames, nil, nil
}

// AllUsers returns the user names granted a given right according to the rules
// of the Access file. AllUsers loads group files as needed by
// calling the provided function to read each file's contents.
func (a *Access) Users(right Right, load func(upspin.PathName) ([]byte, error)) ([]upspin.UserName, error) {
	for {
		readers, neededGroups, err := a.usersNoGroupLoad(right)
		if err != nil {
			return nil, err
		}
		if neededGroups == nil {
			return readers, nil
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
}

// mergeUsers merges src into dst skipping duplicates and returns the updated dst.
func mergeUsers(dst []upspin.UserName, src []upspin.UserName) []upspin.UserName {
	known := make(map[upspin.UserName]bool)
	for _, d := range dst {
		known[d] = true
	}
	for _, s := range src {
		if _, found := known[s]; !found {
			dst = append(dst, s)
		}
	}
	return dst
}

// MarshalJSON returns a JSON-encoded representation of this Access struct.
func (a *Access) MarshalJSON() ([]byte, error) {
	// We need to export a field of Access but we don't want to make it public,
	// so we encode it separately.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(a.list); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON returns an Access given its path name and its JSON encoding.
func UnmarshalJSON(name upspin.PathName, jsonAccess []byte) (*Access, error) {
	var list [numRights][]path.Parsed
	err := json.Unmarshal(jsonAccess, &list)
	if err != nil {
		return nil, err
	}
	access := &Access{
		list: list,
	}
	access.parsed, err = path.Parse(name)
	if err != nil {
		return nil, err
	}
	access.owner = access.parsed.User()
	_, access.domain, err = path.UserAndDomain(access.parsed.User())
	if err != nil {
		return nil, err
	}
	return access, nil
}

// inList reports whether the requester is present in the list, either directly or by wildcard.
// If we encounter a new group in the list, we add it the list of groups to check and will
// process it in another call from Can.
func (a *Access) inList(requester path.Parsed, list []path.Parsed, groupsToCheck []path.Parsed) (bool, []path.Parsed) {
	_, domain, err := path.UserAndDomain(requester.User())
	// We don't expect an error since it's been parsed, but check anyway.
	if err != nil {
		return false, nil
	}
Outer:
	for _, allowed := range list {
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
