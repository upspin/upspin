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
	user  upspin.User
	store upspin.Store
}

var _ upspin.Client = (*Client)(nil)

var loc0 = upspin.Location{}

// TODO: Where does the client get the context?
type Context string

func (c Context) Name() string {
	return string(c)
}

var testContext = Context("testcontext")

var _ upspin.ClientContext = (*Context)(nil)

// New returns a new Client. The arguments are the user service to look up root
// directories for users and the store service in which to store files.
func New(user upspin.User, store upspin.Store) *Client {
	return &Client{
		user:  user,
		store: store,
	}
}

func (c *Client) rootDir(name upspin.PathName) (upspin.Directory, error) {
	// Add a final slash in case it's just a user name and we're referencing the root.
	parsed, err := path.Parse(name + "/")
	if err != nil {
		return nil, err
	}
	endpoints, err := c.user.Lookup(upspin.UserName(parsed.User))
	if err != nil {
		return nil, err
	}
	var dir upspin.Directory
	for _, e := range endpoints {
		dir, err = access.BindDirectory(testContext, e)
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

	// where is the list of locations to examine. It is updated in the loop.
	where := []upspin.Location{entry.Location}
	for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
		loc := where[i]
		// TODO: Be able to connect to another Store.
		if loc.Endpoint != c.store.Endpoint() {
			return nil, fmt.Errorf("TODO: testclient can't handle different store address: %v %v", loc.Endpoint, c.store.Endpoint())
		}
		// TODO: Be able to connect to another Store. Plus don't hack in "in-process".
		if loc.Endpoint.Transport != upspin.InProcess {
			fmt.Printf("%+v\n", loc)
			return nil, fmt.Errorf("TODO: testclient can't handle different store transport: %q %q", loc.Endpoint.Transport, "in-process")
		}
		cipher, locs, err := c.store.Get(entry.Location.Reference.Key)
		if err != nil {
			// An error occurred. Remember the first error we see.
			if firstError == nil {
				firstError = err
			}
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			// Encrypted data was found. Need to unpack it.
			packer := pack.Lookup(entry.Location.Reference.Packing)
			if packer == nil {
				return nil, fmt.Errorf("testclient: unrecognized Packing %d for %q", entry.Location.Reference.Packing, name)
			}
			clearLen := packer.UnpackLen(cipher, nil)
			if clearLen < 0 {
				return nil, fmt.Errorf("testclient: UnpackLen failed for %q", name)
			}
			cleartext := make([]byte, clearLen)
			// Must use a canonicalized name. TODO: Put this in package path?
			parsed, _ := path.Parse(name) // Known to be error-free.
			n, err := packer.Unpack(cleartext, cipher, nil, parsed.Path())
			if err != nil {
				return nil, err
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
