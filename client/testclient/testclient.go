// Package testclient implements a simple client service.
package testclient

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/store/teststore"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Client is a simple non-persistent implementation of upspin.Client suitable for testing.
type Client struct {
	dir   upspin.Directory
	store upspin.Store
}

var _ upspin.Client = (*Client)(nil)

// TODO: Signature should just be a User, or maybe not even that.
func New(dir upspin.Directory, store upspin.Store) *Client {
	return &Client{
		dir:   dir,
		store: store,
	}
}

func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	entry, err := c.dir.Lookup(name)
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
		if loc.Endpoint.Transport != "in-process" {
			fmt.Printf("%+v\n", loc)
			return nil, fmt.Errorf("TODO: testclient can't handle different store transport: %q %q", loc.Endpoint.Transport, "in-process")
		}
		cipher, locs, err := c.store.Get(entry.Location)
		if err != nil {
			// An error occurred. Remember the first error we see.
			if firstError == nil {
				firstError = err
			}
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			// Encrypted data was found.
			// Need to unpack it.
			// TODO: Where is Packing defined? We assume the debug packing.
			blobName, data, err := teststore.UnpackBlob(cipher)
			if err != nil {
				return nil, err
			}
			if blobName != name {
				return nil, fmt.Errorf("%q: got wrong file name: %s", name, blobName)
			}
			return data, nil
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
	return c.dir.Put(name, data, nil) // TODO packdata
}

func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return c.dir.MakeDirectory(dirName)
}
