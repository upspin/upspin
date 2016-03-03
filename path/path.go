// Package path provides tools for parsing and printing file names.
package path

import (
	"bytes"
	"fmt"
	"strings"

	"upspin.googlesource.com/upspin.git/upspin"
)

// Parsing of file names. File names always start with a user name in mail-address form,
// followed by a slash and a possibly empty pathname that follows. Thus the root of
// user@google.com's name space is "user@google.com/".

// Parsed represents a successfully parsed path name.
type Parsed struct {
	User  upspin.UserName // Must be present and non-empty.
	Elems []string        // If empty, refers to the root for the user.
}

func (p Parsed) String() string {
	var b bytes.Buffer
	b.WriteString(string(p.User))
	p.filePath(&b)
	return b.String()
}

// FilePath returns just the path under the root directory part of the
// pathname, without the leading user name.
func (p Parsed) FilePath() string {
	var b bytes.Buffer
	p.filePath(&b)
	return b.String()
}

func (p Parsed) filePath(b *bytes.Buffer) {
	b.WriteByte('/')
	lim := len(p.Elems) - 1
	for i, elem := range p.Elems {
		b.WriteString(string(elem))
		if i < lim {
			b.WriteByte('/')
		}
	}
}

// Path is a helper that returns the string representation with type Name.
func (p Parsed) Path() upspin.PathName {
	return upspin.PathName(p.String())
}

var (
	pn0 = Parsed{}
)

// NameError gives information about an erroneous path name, including the name and error description.
type NameError struct {
	name  string
	error string
}

// Name is the path name that caused the error.
func (n NameError) Name() string {
	return n.name
}

// Error is the implementation of the error interface for NameError.
func (n NameError) Error() string {
	return n.error
}

// Parse parses a full file name, including the user, validates it,
// and returns its parsed form.
func Parse(pathName upspin.PathName) (Parsed, error) {
	name := string(pathName)
	// Pull off the user name.
	slash := strings.IndexByte(name, '/')
	if slash < 0 {
		// No slash.
		return pn0, NameError{string(pathName), "no slash in path"}
	}
	if slash < 6 {
		// No user name. Must be at least "u@x.co". Silly test - do more.
		return pn0, NameError{string(pathName), "no user name in path"}
	}
	user, name := name[:slash], name[slash:]
	if strings.Count(user, "@") != 1 {
		// User name must contain exactly one "@".
		return pn0, NameError{string(pathName), "bad user name in path"}
	}
	p := Parsed{
		User: upspin.UserName(user),
	}
	// Allocate the elems slice all at once, to avoid reallocation in append.
	elems := make([]string, 0, strings.Count(string(pathName), "/"))
	// Split into elements. Don't call strings.Split because it allocates. Also we
	// can process . and .. in this loop.
	for {
		for len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
		if name == "" {
			break
		}
		var i int
		for i = 0; i < len(name); i++ {
			if name[i] == '/' {
				break
			}
		}
		// Handle "." and "..".
		switch name[:i] {
		case ".":
			// Do nothing.
		case "..":
			// Drop previous element.
			if len(elems) > 0 {
				elems = elems[:len(elems)-1]
			}
		default:
			elems = append(elems, name[:i])
		}
		name = name[i:]
		if name == "" {
			break
		}
	}
	for _, elem := range elems {
		if len(elem) > 255 {
			return pn0, NameError{string(pathName), "name element too long"}
		}
	}
	p.Elems = elems
	return p, nil
}

// First returns a parsed name with only the first n elements after the user name.
func (p Parsed) First(n int) Parsed {
	p.Elems = p.Elems[:n]
	return p
}

// Drop returns a parsed name with the last n elements dropped.
func (p Parsed) Drop(n int) Parsed {
	p.Elems = p.Elems[:len(p.Elems)-n]
	return p
}

// Join a new path element to an upspin name.
func Join(path upspin.PathName, elem string) upspin.PathName {
	if len(path) == 0 {
		return upspin.PathName(elem)
	}
	if len(elem) == 0 {
		return path
	}
	return upspin.PathName(fmt.Sprintf("%s/%s", string(path), elem))
}
