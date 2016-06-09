// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package client implements a simple client service talking to services
// running anywhere (GCP, InProcess, etc).
package client

import (
	"fmt"
	"strings"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/common/file"
	"upspin.io/pack"
	"upspin.io/pack/ee"
	"upspin.io/path"
	"upspin.io/upspin"

	_ "upspin.io/pack/plain" // Plain packer used when encoding an Access file.
)

// Client implements upspin.Client.
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

// Put implements upspin.Client.
func (c *Client) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	dir, err := c.Directory(name)
	if err != nil {
		return zeroLoc, err
	}

	_, err = path.Parse(name)
	if err != nil {
		return zeroLoc, err
	}

	var packer upspin.Packer
	if access.IsAccessFile(name) || access.IsGroupFile(name) {
		packer = pack.Lookup(upspin.PlainPack)
	} else {
		// Encrypt data according to the preferred packer
		// TODO: Do a Lookup in the parent directory to find the overriding packer.
		packer = pack.Lookup(c.context.Packing)
		if packer == nil {
			return zeroLoc, fmt.Errorf("unrecognized Packing %d for %q", c.context.Packing, name)
		}
	}

	de := &upspin.DirEntry{
		Name: name,
		Metadata: upspin.Metadata{
			Time:     upspin.Now(),
			Sequence: 0, // Don't care for now.
			Size:     uint64(len(data)),
		},
	}

	var cipher []byte

	// Get a buffer big enough for this data
	cipherLen := packer.PackLen(c.context, data, de)
	if cipherLen < 0 {
		return zeroLoc, fmt.Errorf("PackLen failed for %q", name)
	}
	cipher = make([]byte, cipherLen)
	n, err := packer.Pack(c.context, cipher, data, de)
	if err != nil {
		return zeroLoc, err
	}
	cipher = cipher[:n]

	// Add other readers from the access file.
	if err := c.addReaders(de, name, packer); err != nil {
		return zeroLoc, err
	}

	// Store contents.
	ref, err := c.context.Store.Put(cipher)
	if err != nil {
		return zeroLoc, err
	}
	de.Location = upspin.Location{
		Endpoint:  c.context.Store.Endpoint(),
		Reference: ref,
	}

	// Record directory entry.
	err = dir.Put(de)

	return de.Location, err
}

func (c *Client) addReaders(de *upspin.DirEntry, name upspin.PathName, packer upspin.Packer) error {
	packerString := packer.String()
	if packerString[0] != 'p' || strings.IndexByte("235", packerString[1]) < 0 { // TODO generalize for more packers when some exist
		return nil
	}

	// Add other readers to Packdata.
	// We do this before "Store contents", so an error return wastes little.
	accessName, err := c.context.Directory.WhichAccess(name)
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
		for {
			var neededGroups []upspin.PathName
			readers, neededGroups, err = acc.Users(access.Read)
			if err == nil {
				break
			}
			if err != access.ErrNeedGroup {
				return err
			}
			for _, group := range neededGroups {
				groupData, err := c.Get(group)
				if err != nil {
					return err
				}
				err = access.AddGroup(group, groupData)
				if err != nil {
					return err
				}
			}
		}
	}
	readersPublicKey := make([]upspin.PublicKey, len(readers)+1)
	readersPublicKey[0] = c.context.Factotum.PublicKey()
	n := 1
	for _, r := range readers {
		_, pubkeys, err := c.context.User.Lookup(r)
		if err != nil || len(pubkeys) < 1 {
			// TODO warn that we can't process one of the readers?
			continue
		}
		for _, pubkey := range pubkeys { // pick first key of correct type
			pubkey = upspin.PublicKey(strings.TrimSpace(string(pubkey)))
			if ee.IsValidKeyForPacker(pubkey, packerString) {
				if pubkey != readersPublicKey[0] { // don't duplicate self
					// TODO(ehg) maybe should check for other duplicates?
					readersPublicKey[n] = pubkey
					n++
				}
				break
			}
		}
	}
	readersPublicKey = readersPublicKey[:n]
	packdata := make([]*[]byte, 1)
	packdata[0] = &de.Metadata.Packdata
	packer.Share(c.context, readersPublicKey, packdata)
	return nil
}

// MakeDirectory implements upspin.Client.
func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	dir, err := c.Directory(dirName)
	if err != nil {
		return zeroLoc, err
	}
	return dir.MakeDirectory(dirName)
}

// Get implements upspin.Client.
func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	dir, err := c.Directory(name)
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
		cipher, locs, err := store.Get(loc.Reference)
		if isError(err) {
			continue // locs guaranteed to be nil.
		}
		if locs == nil && err == nil {
			// Encrypted data was found. Need to unpack it.
			// TODO(p,edpin): change when GCP makes the indirected reference
			// have the correct packing info.
			packer := pack.Lookup(entry.Metadata.Packing())
			if packer == nil {
				return nil, fmt.Errorf("client: unrecognized Packing %d for %q", entry.Metadata.Packing(), name)
			}
			clearLen := packer.UnpackLen(c.context, cipher, entry)
			if clearLen < 0 {
				return nil, fmt.Errorf("client: UnpackLen failed for %q", name)
			}
			cleartext := make([]byte, clearLen)
			n, err := packer.Unpack(c.context, cleartext, cipher, entry)
			if err != nil {
				return nil, err // Showstopper.
			}
			return cleartext[:n], nil
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
	// TODO: custom error types.
	if firstError != nil {
		return nil, firstError
	}
	return nil, fmt.Errorf("client: %q not found on any store server", name)
}

// Glob implements upspin.Client.
func (c *Client) Glob(pattern string) ([]*upspin.DirEntry, error) {
	dir, err := c.Directory(upspin.PathName(pattern))
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

// Directory implements upspin.Client.
func (c *Client) Directory(name upspin.PathName) (upspin.Directory, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	var endpoints []upspin.Endpoint
	if parsed.User() == c.context.UserName {
		endpoints = append(endpoints, c.context.Directory.Endpoint())
	}
	if eps, _, err := c.context.User.Lookup(parsed.User()); err == nil {
		endpoints = append(endpoints, eps...)
	}
	var dir upspin.Directory
	for _, e := range endpoints {
		dir, err = bind.Directory(c.context, e)
		if dir != nil {
			return dir, nil
		}
	}
	if err == nil {
		err = fmt.Errorf("client: no endpoint for user %q", parsed.User())
	}
	return nil, err
}

// PublicKeys implements upspin.PublicKeys.
func (c *Client) PublicKeys(name upspin.PathName) ([]upspin.PublicKey, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	var pubKeys []upspin.PublicKey
	if parsed.User() == c.context.UserName {
		pubKeys = append(pubKeys, c.context.Factotum.PublicKey())
	}
	if _, pks, err := c.context.User.Lookup(parsed.User()); err == nil {
		pubKeys = append(pubKeys, pks...)
	}
	if len(pubKeys) == 0 {
		return nil, fmt.Errorf("client: no public keys for user %q", parsed.User())
	}
	return pubKeys, nil
}

// Link implements upspin.Link. This is more a copy on write than a Unix style Link. As soon as
// one of the two files is written, then will diverge.
func (c *Client) Link(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
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

	oldDir, err := c.Directory(oldName)
	if err != nil {
		return nil, err
	}
	entry, err := oldDir.Lookup(oldName)
	if err != nil {
		return nil, err
	}

	packer := pack.Lookup(entry.Metadata.Packing())
	if packer == nil {
		return nil, fmt.Errorf("unrecognized Packing %d for %q", c.context.Packing, oldName)
	}
	if access.IsAccessFile(newName) || access.IsGroupFile(newName) {
		if entry.Metadata.Packing() != upspin.PlainPack {
			return nil, fmt.Errorf("can only link plain packed files to access or group files")
		}
	}

	// Rewrap reader keys only if changing directory.
	if !oldParsed.Drop(1).Equal(newParsed.Drop(1)) {
		if err := c.addReaders(entry, newName, packer); err != nil {
			return nil, err
		}
	}

	// Get the destination upspin.Directory.
	newDir := oldDir
	if oldParsed.User() != newParsed.User() {
		newDir, err = c.Directory(oldName)
		if err != nil {
			return nil, err
		}
	}

	// Update the directory entry with the new name and sequence.
	// If we are linking, the new file must not exist.
	// TODO: Should it also not exist on a rename?
	if rename {
		entry.Metadata.Sequence = upspin.SeqIgnore
	} else {
		entry.Metadata.Sequence = upspin.SeqNotExist
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
