// Package access parses access control files.
package access

import (
	"fmt"
	"log"
	"strings"

	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	// Name of Access file. Exported in case it's useful on its own. Prefer using IsAccessFile below.
	AccessFile = "Access"
)

// Parsed contains the parsed path names found in the ACL file, one for each section.
type Parsed struct {
	Readers   []path.Parsed
	Writers   []path.Parsed
	Appenders []path.Parsed
}

func Parse(data string) (*Parsed, error) {
	lines := strings.Split(data, "\n")

	// Map of access types ("r", "w", "a") to list of paths
	aclEntries := make(map[string][]string, len(lines))

	for i, line := range lines {
		line = cleanUp(line)
		if len(line) == 0 {
			continue
		}
		entry := strings.Split(line, ":")
		if len(entry) != 2 { // we expect format: "r: bob@foo.com,..."
			return nil, fmt.Errorf("invalid format on line %d: %s", i, line)
		}
		accessType := trim(entry[0])
		toAdd := strings.Split(entry[1], ",")
		if _, found := aclEntries[accessType]; found {
			aclEntries[accessType] = append(aclEntries[accessType], toAdd...)
		} else {
			aclEntries[accessType] = toAdd
		}
	}
	// We now have a map of access types such as "r", "w", "a" to
	// a list of possible user names. Interpret the elements now.
	parsed := &Parsed{}

	var readersLeft []string
	parsed.Readers, readersLeft = parsePaths(aclEntries["r"])
	delete(aclEntries, "r")
	var writersLeft []string
	parsed.Writers, writersLeft = parsePaths(aclEntries["w"])
	delete(aclEntries, "w")
	var appendersLeft []string
	parsed.Appenders, appendersLeft = parsePaths(aclEntries["a"])
	delete(aclEntries, "a")

	if len(aclEntries) > 0 {
		return nil, fmt.Errorf("unrecognized text in %s file: %v", AccessFile, aclEntries)
	}

	// TODO: Resolve groups, if any, which are names in the various *Left lists above.
	if len(readersLeft) > 0 || len(writersLeft) > 0 || len(appendersLeft) > 0 {
		log.Printf("Unresolved groups exist")
	}

	return parsed, nil
}

// IsAccessFile returns true if the pathName is a valid upspin path
// name and is for a file named Access, which is special. For
// convenience, it also returns the parsed path in case there were no
// errors parsing it.
func IsAccessFile(pathName upspin.PathName) (bool, *path.Parsed) {
	p, err := path.Parse(pathName)
	if err != nil {
		return false, nil
	}
	n := len(p.Elems)
	return n > 0 && p.Elems[n-1] == AccessFile, &p
}

// cleanUp removes comments and trims whitespace around any remaining characters.
func cleanUp(line string) string {
	return trim(strings.Split(strings.Split(line, "#")[0], "//")[0])
}

// trim is a shortcut to strings.Trim(line, "\t ")
func trim(line string) string {
	return strings.Trim(line, "\t ")
}

// parsePaths parses each path in paths and puts them into a
// path.Parsed structure. Any unparseable path is returned
// separately. Groups (fully-qualified paths) are also returned as
// unparseable for now (TODO: return a group structure)
func parsePaths(paths []string) ([]path.Parsed, []string) {
	parsed := make([]path.Parsed, 0, len(paths))
	leftOvers := make([]string, 0, len(paths))

	for _, pathName := range paths {
		// Check that pathName is a valid Upspin pathName, even if just the userName part.
		p, err := path.Parse(upspin.PathName(trim(pathName) + "/"))
		if err != nil || len(p.Elems) > 0 {
			leftOvers = append(leftOvers, pathName)
			continue
		}
		parsed = append(parsed, p)
	}
	return parsed, leftOvers
}
