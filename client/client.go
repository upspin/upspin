// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package client implements a simple client service talking to services
// running anywhere (GCP, InProcess, etc).
package client

import (
	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/file"
	"upspin.io/errors"
	"upspin.io/key/usercache"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"

	_ "upspin.io/pack/plain" // Plain packer used when encoding an Access file.
)

// Client implements upspin.Client.
type Client struct {
	context upspin.Context
	user    upspin.KeyServer
}

var _ upspin.Client = (*Client)(nil)

const maxBlockSize = 1024 * 1024

// New creates a Client. The client finds the servers according to the given Context.
func New(context upspin.Context) upspin.Client {
	return &Client{
		context: usercache.Global(context),
	}
}

// Put implements upspin.Client.
func (c *Client) Put(name upspin.PathName, data []byte) (*upspin.DirEntry, error) {
	return c.put(name, data, upspin.AttrNone)
}

// PutLink implements upspin.Client.
func (c *Client) PutLink(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return c.put(newName, []byte(oldName), upspin.AttrLink)
}

func (c *Client) put(name upspin.PathName, data []byte, attr upspin.FileAttributes) (*upspin.DirEntry, error) {
	const op = "Put"
	dir, err := c.DirServer(name)
	if err != nil {
		return nil, errors.E(op, err)
	}

	_, err = path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}

	var packer upspin.Packer
	if access.IsAccessFile(name) || access.IsGroupFile(name) {
		packer = pack.Lookup(upspin.PlainPack)
	} else {
		// Encrypt data according to the preferred packer
		// TODO: Do a Lookup in the parent directory to find the overriding packer.
		packer = pack.Lookup(c.context.Packing())
		if packer == nil {
			return nil, errors.E(op, name, errors.Errorf("unrecognized Packing %d", c.context.Packing()))
		}
	}

	de := &upspin.DirEntry{
		Name:     name,
		Packing:  packer.Packing(),
		Time:     upspin.Now(),
		Sequence: 0, // Don't care for now.
		Writer:   c.context.UserName(),
		Attr:     attr,
	}

	// Start the I/O.
	store, err := bind.StoreServer(c.context, c.context.StoreEndpoint())
	if err != nil {
		return nil, err
	}
	bp, err := packer.Pack(c.context, de)
	if err != nil {
		return nil, err
	}
	for len(data) > 0 {
		n := len(data)
		if n > maxBlockSize {
			n = maxBlockSize
		}
		cipher, err := bp.Pack(data[:n])
		if err != nil {
			return nil, errors.E(op, err)
		}
		data = data[n:]
		ref, err := store.Put(cipher)
		if err != nil {
			return nil, errors.E(op, err)
		}
		bp.SetLocation(
			upspin.Location{
				Endpoint:  c.context.StoreEndpoint(),
				Reference: ref,
			},
		)
	}
	err = bp.Close()
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Add other readers from the access file.
	if err := c.addReaders(de, name, packer); err != nil {
		return nil, errors.E(op, err)
	}

	// Record directory entry.
	err = dir.Put(de)
	if err != nil {
		return nil, errors.E(op, err)
	}

	return de, nil
}

func (c *Client) addReaders(de *upspin.DirEntry, name upspin.PathName, packer upspin.Packer) error {
	if packer.String() != "ee" {
		return nil
	}
	directory, err := bind.DirServer(c.context, c.context.DirEndpoint())
	if err != nil {
		return err
	}

	// Add other readers to Packdata.
	// We do this before "Store contents", so an error return wastes little.
	accessName, err := directory.WhichAccess(name)
	if err != nil {
		return err
	}
	var readers []upspin.UserName
	if accessName != "" {
		accessData, err := c.Get(accessName)
		if err != nil {
			return err
		}
		acc, err := access.Parse(accessName, accessData)
		if err != nil {
			return err
		}
		readers, err = acc.Users(access.Read, c.Get)
	}
	readersPublicKey := make([]upspin.PublicKey, len(readers)+1)
	readersPublicKey[0] = c.context.Factotum().PublicKey()
	n := 1
	for _, r := range readers {
		u, err := c.context.KeyServer().Lookup(r)
		if err != nil || len(u.PublicKey) == 0 {
			// TODO warn that we can't process one of the readers?
			continue
		}
		if u.PublicKey != readersPublicKey[0] { // don't duplicate self
			// TODO(ehg) maybe should check for other duplicates?
			readersPublicKey[n] = u.PublicKey
			n++
		}
	}
	readersPublicKey = readersPublicKey[:n]
	packdata := make([]*[]byte, 1)
	packdata[0] = &de.Packdata
	packer.Share(c.context, readersPublicKey, packdata)
	return nil
}

// MakeDirectory implements upspin.Client.
func (c *Client) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	dir, err := c.DirServer(dirName)
	if err != nil {
		return nil, err
	}
	return dir.MakeDirectory(dirName)
}

// Get implements upspin.Client.
func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	const op = "Get"
	dir, err := c.DirServer(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	entry, err := dir.Lookup(name)
	if err != nil {
		return nil, errors.E(op, err)
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

	var data []byte
	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.E(op, name, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	bu, err := packer.Unpack(c.context, entry)
	if err != nil {
		return nil, errors.E(op, name, err) // Showstopper.
	}
Blocks:
	for b := 0; ; b++ {
		block, ok := bu.NextBlock()
		if !ok {
			break // EOF
		}
		// Get the data for this block.
		// where is the list of locations to examine. It is updated in the loop.
		where := []upspin.Location{block.Location}
		for i := 0; i < len(where); i++ { // Not range loop - where changes as we run.
			loc := where[i]
			store, err := bind.StoreServer(c.context, loc.Endpoint)
			if isError(err) {
				continue
			}
			cipher, locs, err := store.Get(loc.Reference)
			if isError(err) {
				continue // locs guaranteed to be nil.
			}
			if locs == nil && err == nil {
				// Found the data. Unpack it.
				clear, err := bu.Unpack(cipher)
				if err != nil {
					return nil, errors.E(op, name, err) // Showstopper.
				}
				data = append(data, clear...) // TODO: Could avoid a copy if only one block.
				continue Blocks
			}
			// Add new locs to the list. Skip ones already there - they've been processed. TODO: n^2.
		outer:
			for _, newLoc := range locs {
				for _, oldLoc := range where {
					if oldLoc == newLoc {
						continue outer
					}
				}
				where = append(where, newLoc)
			}
		}
		// If we arrive here, we have failed to find a block.
		// TODO: custom error types.
		if firstError != nil {
			return nil, errors.E(op, name, firstError)
		}
		return nil, errors.Errorf("client: data for block %d in %q not found on any store server", b, name)
	}
	return data, nil
}

// Glob implements upspin.Client.
func (c *Client) Glob(pattern string) ([]*upspin.DirEntry, error) {
	dir, err := c.DirServer(upspin.PathName(pattern))
	if err != nil {
		return nil, err
	}
	return dir.Glob(pattern)
}

// Create implements upspin.Client.
func (c *Client) Create(name upspin.PathName) (upspin.File, error) {
	// TODO: Make sure directory exists?
	return file.Writable(c, name), nil
}

// Open implements upspin.Client.
func (c *Client) Open(name upspin.PathName) (upspin.File, error) {
	data, err := c.Get(name)
	if err != nil {
		return nil, err
	}
	return file.Readable(c, name, data), nil
}

// DirServer implements upspin.Client.
func (c *Client) DirServer(name upspin.PathName) (upspin.DirServer, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	var endpoints []upspin.Endpoint
	if parsed.User() == c.context.UserName() {
		endpoints = append(endpoints, c.context.DirEndpoint())
	}
	if u, err := c.context.KeyServer().Lookup(parsed.User()); err == nil {
		endpoints = append(endpoints, u.Dirs...)
	}
	var dir upspin.DirServer
	for _, e := range endpoints {
		dir, err = bind.DirServer(c.context, e)
		if dir != nil {
			return dir, nil
		}
	}
	if err == nil {
		err = errors.Errorf("client: no endpoint for user %q", parsed.User())
	}
	return nil, err
}

// PutDuplicate implements upspin.Link. This is more a copy on write than a Unix style Link. As soon as
// one of the two files is written, then will diverge.
func (c *Client) PutDuplicate(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return c.linkOrRename(oldName, newName, false)
}

// Rename implements upspin.Rename.  Performed by linking to the new name and deleting the old one.
func (c *Client) Rename(oldName, newName upspin.PathName) error {
	_, err := c.linkOrRename(oldName, newName, true)
	return err
}

func (c *Client) linkOrRename(oldName, newName upspin.PathName, rename bool) (*upspin.DirEntry, error) {
	newParsed, err := path.Parse(newName)
	if err != nil {
		return nil, err
	}
	oldParsed, err := path.Parse(oldName)
	if err != nil {
		return nil, err
	}

	oldDir, err := c.DirServer(oldName)
	if err != nil {
		return nil, err
	}
	entry, err := oldDir.Lookup(oldName)
	if err != nil {
		return nil, err
	}
	if entry.IsDir() {
		return nil, errors.Errorf("cannot link or rename directories")
	}

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.Errorf("unrecognized Packing %d for %q", c.context.Packing(), oldName)
	}
	if access.IsAccessFile(newName) || access.IsGroupFile(newName) {
		if entry.Packing != upspin.PlainPack {
			return nil, errors.Errorf("can only link plain packed files to access or group files")
		}
	}

	// Rewrap reader keys only if changing directory.
	if !oldParsed.Drop(1).Equal(newParsed.Drop(1)) {
		if err := c.addReaders(entry, newName, packer); err != nil {
			return nil, err
		}
	}

	// Get the destination upspin.DirServer.
	newDir := oldDir
	if oldParsed.User() != newParsed.User() {
		newDir, err = c.DirServer(newName)
		if err != nil {
			return nil, err
		}
	}

	// Update the directory entry with the new name and sequence.
	// If we are linking, the new file must not exist.
	// TODO: Should it also not exist on a rename?
	if rename {
		entry.Sequence = upspin.SeqIgnore
	} else {
		entry.Sequence = upspin.SeqNotExist
	}
	if err := packer.Name(c.context, entry, newName); err != nil {
		return nil, err
	}

	// Record directory entry.
	if err := newDir.Put(entry); err != nil {
		return nil, err
	}

	if rename {
		// Remove original entry.
		if err := oldDir.Delete(oldName); err != nil {
			return entry, err
		}
	}
	return entry, nil
}
