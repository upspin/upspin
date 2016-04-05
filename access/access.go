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
	"errors"
	"fmt"
	"strings"

	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	// accessFile is the name of the Access file.
	accessFile = "Access"
)

// Parsed contains the parsed path names found in the ACL file, one for each section.
type Parsed struct {
	Readers   []path.Parsed
	Writers   []path.Parsed
	Appenders []path.Parsed
}

const (
	invalidRight = iota
	readRight
	writeRight
	appendRight
)

const (
	invalidFormat = "%s:%d: unrecognized text: %q"
)

// Parse parses the contents of the path name, in data, and returns the parsed contents.
func Parse(pathName upspin.PathName, data []byte) (*Parsed, error) {
	var p Parsed
	// Temporaries. Pre-allocate so they can be reused in the loop, saving allocations.
	rights := make([][]byte, 10)
	users := make([][]byte, 10)
	s := bufio.NewScanner(bytes.NewReader(data))
	for lineNum := 1; s.Scan(); lineNum++ {
		line := s.Bytes()

		// Remove comments.
		if index := bytes.IndexByte(line, '#'); index >= 0 {
			line = line[:index]
		}

		// Ignore blank lines.
		line = bytes.TrimSpace(line)
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
			switch which(right) {
			case readRight:
				p.Readers, err = parsedAppend(p.Readers, users...)
			case writeRight:
				p.Writers, err = parsedAppend(p.Writers, users...)
			case appendRight:
				p.Appenders, err = parsedAppend(p.Appenders, users...)
			case invalidRight:
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
	return &p, nil
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\r', '\f', '\v', '\n', '\t':
		return true
	default:
		return false
	}
}

// parsedAppend parses the users (as path.Parse values) and appends them to the list.
func parsedAppend(list []path.Parsed, users ...[]byte) ([]path.Parsed, error) {
	for _, user := range users {
		p, err := path.Parse(upspin.PathName(user) + "/")
		if err != nil {
			// TODO: should do syntax check of group names if path.Parse fails.
			continue
		}
		if len(p.Elems) > 0 {
			// TODO: can't handle groups yet; ignore.
			continue
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
func which(right []byte) int {
	for i, c := range right {
		right[i] = toLower(c)
	}
	switch right[0] {
	case 'r':
		if len(right) == 1 || bytes.Equal(right, []byte("read")) {
			return readRight
		}
	case 'w':
		if len(right) == 1 || bytes.Equal(right, []byte("write")) {
			return writeRight
		}
	case 'a':
		if len(right) == 1 || bytes.Equal(right, []byte("append")) {
			return appendRight
		}
	}
	return invalidRight
}

// IsAccessFile reports whether the pathName contains a file named Access, which is special.
func IsAccessFile(pathName upspin.PathName) bool {
	return strings.HasSuffix(string(pathName), accessFile)
}

// HasAccess reports whether a given user has access to a path, given a slice of allowed users with that access. The
// slice of allowed users may contain only the following special wildcards: "*" which means anyone has access
// and "*@<domain>" which means any one from that domain has access.
func HasAccess(user upspin.UserName, parsedPath path.Parsed, allowedAccess []upspin.UserName) (bool, error) {
	// First, if user is the owner, access is granted.
	if user == parsedPath.User {
		return true, nil
	}
	// Save space for this user's domain if we need to match wildcards, but process it lazily.
	var userDomain string
	for _, u := range allowedAccess {
		if u == user {
			return true, nil
		}
		// We interpret "*" and "*@<domain>" specially.
		if strings.HasPrefix(string(u), "*") {
			if u == "*" {
				// Everyone has access.
				return true, nil
			}
			pos := strings.IndexByte(string(u), '@')
			if pos != 1 {
				// This should never happen if we took allowedAccess from a valid Metadata entry.
				return false, errors.New("malformed user name")
			}
			// We now need user and access domains.
			var err error
			if userDomain == "" {
				_, userDomain, err = path.UserAndDomain(user)
				if err != nil {
					// This should never happen if we took allowedAccess from a valid Metadata entry.
					return false, err
				}
			}
			_, accessDomain, err := path.UserAndDomain(u)
			if err != nil {
				// This should never happen if we took allowedAccess from a valid Metadata entry.
				return false, err
			}
			// Both userDomain and ownerDomain are guaranteed not empty at this point.
			if userDomain == accessDomain {
				return true, nil
			}
		}
	}
	return false, nil
}
