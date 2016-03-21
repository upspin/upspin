// Package path provides tools for parsing and printing file names.
package path

import (
	"bytes"
	"errors"
	"strings"

	gopath "path"

	"upspin.googlesource.com/upspin.git/upspin"
)

// Parsing of file names. File names always start with a user name in mail-address form,
// followed by a slash and a possibly empty pathname that follows. Thus the root of
// user@google.com's name space is "user@google.com/". But Parse also allows
// "user@google.com" to refer to the user's root directory.

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
	pn0         = Parsed{}
	errUserName = errors.New("user name not properly formatted")
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
// and returns its parsed form. If the name is a user root directory,
// the trailing slash is optional.
func Parse(pathName upspin.PathName) (Parsed, error) {
	name := string(pathName)
	// Pull off the user name.
	var user string
	slash := strings.IndexByte(name, '/')
	if slash < 0 {
		user, name = name, ""
	} else {
		user, name = name[:slash], name[slash:]
	}
	if len(user) < 6 {
		// No user name. Must be at least "u@x.co". Silly test - do more.
		return pn0, NameError{string(pathName), "no user name in path"}
	}
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

// Join appends any number of path elements onto a (possibly empty)
// Upspin path, adding a separating slash if necessary. All empty
// strings are ignored. The result, if non-empty, is passed through
// Clean. There is no guarantee that the resulting path is a valid
// Upspin path. This differs from path.Join in that it requires a
// first argument of type upspin.PathName.
func Join(path upspin.PathName, elems ...string) upspin.PathName {
	// Do what we can to avoid unnecessary allocation.
	joined := upspin.PathName("")
	for i, e := range elems {
		if e != "" {
			joined = upspin.PathName(strings.Join(elems[i:], "/"))
			break
		}
	}
	switch {
	case path == "" && joined == "":
		return ""
	case path == "" && joined != "":
		// Nothing to do.
	case path != "" && joined == "":
		joined = path
	case path != "" && joined != "":
		joined = path + "/" + joined
	}
	return Clean(joined)
}

// Clean applies Go's path.Clean to an Upspin path.
func Clean(path upspin.PathName) upspin.PathName {
	return upspin.PathName(gopath.Clean(string(path)))
}

// UserAndDomain splits an upspin.UserName into user and domain and returns the pair.
func UserAndDomain(userName upspin.UserName) (user string, domain string, err error) {
	u := string(userName)
	if strings.Count(u, "@") != 1 {
		return "", "", errUserName
	}
	if strings.Count(u, "/") != 0 {
		return "", "", errUserName
	}
	i := strings.IndexByte(u, '@')
	user = u[:i]
	if len(user) < 1 {
		return "", "", errUserName
	}
	domain = u[i+1:]
	if len(domain) < 4 {
		return "", "", errUserName
	}
	if strings.Count(domain, ".") < 1 {
		return "", "", errUserName
	}
	return
}
