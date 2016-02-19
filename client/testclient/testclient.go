// Package testclient implements a simple client service.
package testclient

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Client is a simple non-persistent implementation of upspin.Client suitable for testing.
type Client struct {
	ctxt *upspin.Context
	user upspin.User
}

var _ upspin.Client = (*Client)(nil)

var loc0 = upspin.Location{}

// New returns a new Client. The argument is the user service to use to look up root
// directories for users.
func New(ctxt *upspin.Context) *Client {
	return &Client{
		ctxt: ctxt,
		user: ctxt.User,
	}
}

func (c *Client) rootDir(name upspin.PathName) (upspin.Directory, error) {
	// Add a final slash in case it's just a user name and we're referencing the root.
	parsed, err := path.Parse(name + "/")
	if err != nil {
		return nil, err
	}
	endpoints, _, err := c.user.Lookup(upspin.UserName(parsed.User))
	if err != nil {
		return nil, err
	}
	var dir upspin.Directory
	for _, e := range endpoints {
		dir, err = access.BindDirectory(c.ctxt, e)
		if dir != nil {
			return dir, nil
		}
	}
	if err == nil {
		err = fmt.Errorf("testclient: no such user %q", parsed.User)
	}
	return nil, err
}

func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	dir, err := c.rootDir(name)
	if err != nil {
		return nil, err
	}
	entry, err := dir.Lookup(name)
	if err != nil {
		return nil, err
	}

	// firstError remembers the first error we saw. If we fail completely we return it.
	var firstError error
	// isError reports whether err is non-nil and remembers it if it is.
	isError := func(err error) bool {
		if err == nil {
			return false
		}
		if firstError == nil {
			firstError = err
		}
		return true
	}

	// where is the list of locations to examine. It is updated in the loop.
	where := []upspin.Location{entry.Location}
	for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
		loc := where[i]
		store, err := access.BindStore(c.ctxt, loc.Endpoint)
		if isError(err) {
			continue
		}
		cipher, locs, err := store.Get(entry.Location.Reference.Key)
		if isError(err) {
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			// Encrypted data was found. Need to unpack it.
			packer := pack.Lookup(entry.Location.Reference.Packing)
			if packer == nil {
				return nil, fmt.Errorf("testclient: unrecognized Packing %d for %q", entry.Location.Reference.Packing, name)
			}
			clearLen := packer.UnpackLen(c.ctxt, cipher, nil)
			if clearLen < 0 {
				return nil, fmt.Errorf("testclient: UnpackLen failed for %q", name)
			}
			cleartext := make([]byte, clearLen)
			// Must use a canonicalized name. TODO: Put this in package path?
			parsed, _ := path.Parse(name) // Known to be error-free.
			n, err := packer.Unpack(c.ctxt, cleartext, cipher, nil, parsed.Path())
			if err != nil {
				return nil, err // Showstopper.
			}
			return cleartext[:n], nil
		}
		// Add new locs to the list. Skip ones already there - they've been processed. TODO: n^2.
		for _, newLoc := range locs {
			for _, oldLoc := range where {
				if newLoc != oldLoc {
					where = append(where, newLoc)
				}
			}
		}
	}
	// TODO: custom error types.
	if firstError != nil {
		return nil, firstError
	}
	return nil, fmt.Errorf("%q not found on any store server", name)
}

func (c *Client) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	dir, err := c.rootDir(name)
	if err != nil {
		return loc0, err
	}
	return dir.Put(name, data, nil) // TODO packdata
}

func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	dir, err := c.rootDir(dirName)
	if err != nil {
		return loc0, err
	}
	return dir.MakeDirectory(dirName)
}
