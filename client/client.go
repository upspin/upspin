// Package client implements a simple client service talking to services
// running anywhere (GCP, InProcess, etc).
package client

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/client/common/file"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

type Client struct {
	context *upspin.Context
}

var _ upspin.Client = (*Client)(nil)

var (
	zeroLoc upspin.Location
)

// New creates a Client. The client finds the servers according to the given Context.
func New(context *upspin.Context) upspin.Client {
	return &Client{
		context: context,
	}
}

func (c *Client) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	dir, err := c.getRootDir(name)
	if err != nil {
		return zeroLoc, err
	}

	// Encrypt data according to the preferred packer
	// TODO: Do a Lookup in the parent directory to find the overriding packer.
	packer := pack.Lookup(c.context.Packing)
	if packer == nil {
		return zeroLoc, fmt.Errorf("unrecognized Packing %d for %q", c.context.Packing, name)
	}

	meta := &upspin.Metadata{}

	// Get a buffer big enough for this data
	cipherLen := packer.PackLen(c.context, data, meta, name)
	if cipherLen < 0 {
		return zeroLoc, fmt.Errorf("PackLen failed for %q", name)
	}
	// TODO: Some packers don't update the meta in PackLen, but some do. If not done, update it now.
	if len(meta.PackData) == 0 {
		meta.PackData = []byte{byte(c.context.Packing)}
	}
	cipher := make([]byte, cipherLen)
	n, err := packer.Pack(c.context, cipher, data, meta, name)
	if err != nil {
		return zeroLoc, err
	}
	cipher = cipher[:n]

	// Store it.
	return dir.Put(name, cipher, meta.PackData, nil) // TODO: Options
}

func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	dir, err := c.getRootDir(dirName)
	if err != nil {
		return zeroLoc, err
	}
	return dir.MakeDirectory(dirName)
}

func (c *Client) getRootDir(name upspin.PathName) (upspin.Directory, error) {
	// Add a final slash in case it's just a user name and we're referencing the root.
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	endpoints, _, err := c.context.User.Lookup(parsed.User)
	if err != nil {
		return nil, err
	}
	var dir upspin.Directory
	for _, e := range endpoints {
		dir, err = bind.Directory(c.context, e)
		if dir != nil {
			return dir, nil
		}
	}
	if err == nil {
		err = fmt.Errorf("client: no endpoint for user %q", parsed.User)
	}
	return nil, err
}

func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	dir, err := c.getRootDir(name)
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
		store, err := bind.Store(c.context, loc.Endpoint)
		if isError(err) {
			continue
		}
		cipher, locs, err := store.Get(loc.Reference.Key)
		if isError(err) {
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			// Encrypted data was found. Need to unpack it.
			packer := pack.Lookup(entry.Location.Reference.Packing)
			if packer == nil {
				return nil, fmt.Errorf("client: unrecognized Packing %d for %q", entry.Location.Reference.Packing, name)
			}
			clearLen := packer.UnpackLen(c.context, cipher, &entry.Metadata)
			if clearLen < 0 {
				return nil, fmt.Errorf("client: UnpackLen failed for %q", name)
			}
			cleartext := make([]byte, clearLen)
			// Must use a canonicalized name. TODO: Put this in package path?
			parsed, _ := path.Parse(name) // Known to be error-free.
			n, err := packer.Unpack(c.context, cleartext, cipher, &entry.Metadata, parsed.Path())
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
	return nil, fmt.Errorf("client: %q not found on any store server", name)
}

func (c *Client) Glob(pattern string) ([]*upspin.DirEntry, error) {
	dir, err := c.getRootDir(upspin.PathName(pattern))
	if err != nil {
		return nil, err
	}
	return dir.Glob(pattern)
}

func (c *Client) Create(name upspin.PathName) (upspin.File, error) {
	// TODO: Make sure directory exists?
	return file.Writable(c, name), nil
}

func (c *Client) Open(name upspin.PathName) (upspin.File, error) {
	data, err := c.Get(name)
	if err != nil {
		return nil, err
	}
	return file.Readable(c, name, data), nil
}
