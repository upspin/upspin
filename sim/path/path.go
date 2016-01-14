package path

import (
	"bytes"
	"path"
	"strings"
)

type UserName string
type Name string

// Parsing of file names. File names always start with a user name in mail-address form,
// followed by a slash and a possibly empty pathname that follows. Thus the root of
// user@google.com's name space is "user@google.com/".

type Parsed struct {
	User  UserName // Must be present and non-empty.
	Elems []string // If empty, refers to the root for the user.
}

func (p Parsed) String() string {
	var b bytes.Buffer
	b.WriteString(string(p.User))
	if len(p.Elems) == 0 {
		b.WriteByte('/')
	} else {
		for _, elem := range p.Elems {
			b.WriteByte('/')
			b.WriteString(string(elem))
		}
	}
	return b.String()
}

var (
	pn0 = Parsed{}
)

type NameError struct {
	name  string
	error string
}

func (n NameError) Name() string {
	return n.name
}

func (n NameError) Error() string {
	return n.error
}

// Parse parses a full file name, including the user, validates it,
// and returns its parsed form.
func Parse(pathName Name) (Parsed, error) {
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
	user, name := name[:slash], path.Clean(name[slash:])
	if strings.Count(user, "@") != 1 {
		// User name must contain exactly one "@".
		return pn0, NameError{string(pathName), "bad user name in path"}
	}
	elems := strings.Split(name, "/") // Include the slash - it's rooted.
	// First element will be empty because we start with a slash: empty string before it.
	elems = elems[1:]
	// There will be a trailing empty element if the name is just /, the root.
	if name == "/" {
		elems = elems[1:]
	}
	for _, elem := range elems {
		if len(elem) > 255 {
			return pn0, NameError{string(pathName), "name element too long"}
		}
	}
	pn := Parsed{
		User:  UserName(user),
		Elems: elems,
	}
	return pn, nil
}

func cleanPath(pathName Name) string {
	return path.Clean(string(pathName))
}
