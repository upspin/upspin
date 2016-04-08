// Package access parses access control files.
//
// Access files have the following format:
// <access type>[, <access type]: <email>[, <email>, ...]
//
// Anything after a '#' character is ignored
//
// Example:
//
// Read: email@domain,com, email2@domain.com
// Write: writer@domain.com, writer2@domain.com, writer3@exmaple,com
// Append,Write: appender@example.com # This is a comment
package access

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	// accessFile is the name of the Access file.
	accessFile = "Access"
)

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
)

// rightNames are the names of the rights, in order (and missing invalid).
var rightNames = [][]byte{
	[]byte("read"),
	[]byte("write"),
	[]byte("list"),
	[]byte("create"),
	[]byte("delete"),
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
	// It is indexed by a right.
	list [][]path.Parsed
}

// Path returns the full path name of the file that was parsed.
func (a *Access) Path() upspin.PathName {
	return a.parsed.Path()
}

// Parse parses the contents of the path name, in data, and returns the parsed Acces.
func Parse(pathName upspin.PathName, data []byte) (*Access, error) {
	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, err
	}
	_, domain, err := path.UserAndDomain(parsed.User)
	// We don't expect an error since it's been parsed, but check anyway.
	if err != nil {
		return nil, err
	}
	a := &Access{
		parsed: parsed,
		owner:  parsed.User,
		domain: domain,
		list:   make([][]path.Parsed, numRights),
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
			return nil, fmt.Errorf("%s:%d: syntax error: no colon on line: %q", pathName, lineNum, line)
		}

		// Parse rights and users lists.
		rightsText := bytes.TrimSpace(line[:colon]) // TrimSpace for good error messages below.
		rights = splitList(rights[:0], rightsText)
		if rights == nil {
			return nil, fmt.Errorf("%s:%d: syntax error: invalid rights list: %q", pathName, lineNum, rightsText)
		}
		usersText := bytes.TrimSpace(line[colon+1:])
		users := splitList(users[:0], usersText)
		if users == nil {
			return nil, fmt.Errorf("%s:%d: syntax error: invalid users list: %q", pathName, lineNum, usersText)
		}

		var err error
		for _, right := range rights {
			switch r := which(right); r {
			case Read, Write, List, Create, Delete:
				a.list[r], err = parsedAppend(a.list[r], parsed.User, users...)
			case Invalid:
				return nil, fmt.Errorf("%s:%d: invalid right: %q", pathName, lineNum, right)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: bad users list %q: %v", pathName, lineNum, usersText, err)
		}
	}
	if s.Err() != nil {
		return nil, s.Err()
	}
	return a, nil
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\r', '\f', '\v', '\n', '\t':
		return true
	default:
		return false
	}
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
				return nil, fmt.Errorf("bad user name in group path %q", user)
			}
			// It has no user name, so it might be a path name for a group file.
			p, err = path.Parse(upspin.PathName(owner) + groupElem + upspin.PathName(user))
			if err != nil {
				return nil, err
			}
		}
		// Check group syntax.
		if len(p.Elems) > 0 {
			// First element must be group.
			if p.Elems[0] != "Group" {
				return nil, fmt.Errorf("illegal group %q", user)
			}
			// Groups cannot be wild cards.
			if bytes.HasPrefix(user, []byte("*@")) {
				return nil, fmt.Errorf("cannot have wildcard for group name %q", user)
			}
		}
		list = append(list, p)
	}
	return list, nil
}

// splitList parses a comma-separated list, ignoring spaces. It returns nil
// if the list is badly formed. We avoid bytes.Split because it allocates.
func splitList(list [][]byte, text []byte) [][]byte {
	// One comma- or EOF-terminated element per iteration.
	for i, j := 0, 0; i < len(text); i = j {
		for j = i; j != len(text) && text[j] != ','; j++ {
		}
		list = append(list, text[i:j])
		if j != len(text) { // Skip the comma.
			j++
		}
	}
	if len(list) == 0 {
		return nil
	}
	for i, elem := range list {
		elem = bytes.TrimSpace(elem)
		if len(elem) == 0 {
			return nil
		}
		// If it's still got spaces, there's trouble.
		// TODO: One day we may need quoted strings for file names with spaces.
		// TODO: There is strings.ContainsAny but not bytes.ContainsAny.
		for _, b := range elem {
			if isSpace(b) {
				return nil
			}
		}
		list[i] = elem
	}
	return list
}

// toLower lower cases a single character.
func toLower(b byte) byte {
	// An old trick: In ASCII the characters line up bitwise so this changes any letter to lower case.
	return b | ('a' - 'A')
}

// which reports which right the text represents. Case is ignored and a right may be
// specified by its first letter only. We know that the text is not empty.
func which(right []byte) Right {
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

// AddGroup installs a group with the specified name and textual contents,
// which should have been read from the group file with that name.
// If the group is already known, its definition is replaced.
// TODO: This doesn't have to be a method as it affects global state. Leave it this way?
func (a *Access) AddGroup(pathName upspin.PathName, contents []byte) error {
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
			return nil, fmt.Errorf("%s:%d: syntax error in group file: %q", parsed, lineNum, line)
		}
		group, err = parsedAppend(group, parsed.User, users...)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: bad group users list %q: %v", parsed, lineNum, line, err)
		}
	}
	if s.Err() != nil {
		return nil, s.Err()
	}
	return group, nil
}

// ErrNeedGroup is returned by Access.Can when a group file must be provided.
var ErrNeedGroup = errors.New("need group")

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
// If the Access file does not know the members of a group that it
// needs to resolve the answer, it return false, sets the error to
// ErrNeedGroup, and returns a list of the group files it needs to
// have read for it. The caller should fetch these and report them
// with the AddGroup method, then retry.
//
func (a *Access) Can(requester upspin.UserName, right Right, pathName upspin.PathName) (bool, []upspin.PathName, error) {
	parsedRequester, err := path.Parse(upspin.PathName(requester + "/"))
	if err != nil {
		return false, nil, err
	}
	// First, if user is the owner and the request is for read access,
	// or write or create access to an Access file, access is granted.
	if requester == a.owner {
		switch right {
		case Read, List:
			// User can always read or list anything in the user's tree.
			return true, nil, nil
		case Write, Create:
			// User always has the right to create or modify an Access file.
			if IsAccessFile(pathName) {
				return true, nil, nil
			}
		}
	}
	var list []path.Parsed
	switch right {
	case Read, Write, List, Create, Delete:
		list = a.list[right]
	default:
		return false, nil, fmt.Errorf("unrecognized right value %d", right)
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
		// TODO: The call to Path allocates. Would be nice to avoid.
		group := groupsToCheck[i]
		groupPath := group.Path()
		mu.RLock()
		known, ok := groups[groupPath]
		mu.RUnlock()
		if !ok {
			missingGroups = append(missingGroups, groupPath)
			continue
		}
		found, groupsToCheck = a.inList(parsedRequester, known, groupsToCheck)
		if found {
			return true, nil, nil
		}
	}
	if missingGroups != nil {
		return false, missingGroups, ErrNeedGroup
	}
	return false, nil, nil
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
	var list [][]path.Parsed
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
	access.owner = access.parsed.User
	_, access.domain, err = path.UserAndDomain(access.parsed.User)
	if err != nil {
		return nil, err
	}
	return access, nil
}

// inList reports whether the requester is present in the list, either directly or by wildcard.
// If we encounter a new group in the list, we add it the list of groups to check and will
// process it in another call from Can.
func (a *Access) inList(requester path.Parsed, list []path.Parsed, groupsToCheck []path.Parsed) (bool, []path.Parsed) {
	_, domain, err := path.UserAndDomain(requester.User)
	// We don't expect an error since it's been parsed, but check anyway.
	if err != nil {
		return false, nil
	}
Outer:
	for _, allowed := range list {
		if len(allowed.Elems) == 0 {
			if allowed.User == requester.User {
				return true, nil
			}
			// Wildcard: The path name *@domain.com matches anyone in domain.
			if strings.HasPrefix(string(allowed.User), "*@") && string(allowed.User[2:]) == domain {
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
