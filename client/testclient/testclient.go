// Package testclient implements a simple client service.
package testclient

import (
	"errors"
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
		// TODO: Need an == for NetAddr and for Location.
		if loc.NetAddr.Addr.String() != c.store.NetAddr().Addr.String() {
			return nil, errors.New("TODO: testclient can't handle different store")
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
		// Add new locs to the list
		// TODO: avoid dups, need == for location.
		where = append(where, locs...)
	}
	// TODO: custom error types.
	if firstError != nil {
		return nil, firstError
	}
	return nil, fmt.Errorf("%q not found on any store server", name)
}

func (c *Client) Put(name upspin.PathName, data, metadata []byte) (upspin.Location, error) {
	// TODO: What to do with metadata?
	return c.dir.Put(name, data)
}

func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return c.dir.MakeDirectory(dirName)
}
