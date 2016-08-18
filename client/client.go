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

	"upspin.io/client/clientutil"
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
	return c.put(name, data, upspin.AttrNone, "")
}

// PutLink implements upspin.Client.
func (c *Client) PutLink(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return c.put(newName, nil, upspin.AttrLink, oldName)
}

func (c *Client) put(name upspin.PathName, data []byte, attr upspin.Attribute, link upspin.PathName) (*upspin.DirEntry, error) {
	const op = "Put"
	dir, err := c.DirServer(name)
	if err != nil {
		return nil, errors.E(op, err)
	}

	parsed, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	name = parsed.Path() // Make sure it's clean.
	if attr == upspin.AttrLink {
		parsed, err := path.Parse(link)
		if err != nil {
			return nil, errors.E(op, err)
		}
		link = parsed.Path() // Make sure it's clean.
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
		Link:     link,
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

	if packer.Packing() == upspin.EEPack && attr == upspin.AttrNone {
		// For EE, update the packing for the other
		// readers as specified by the Access file.
		if err := c.addReaders(de, name, packer); err != nil {
			return nil, errors.E(op, err)
		}
	}

	// Record directory entry.
	_, err = dir.Put(de)
	if err != nil {
		// TODO: Implement links.
		return nil, errors.E(op, err)
	}

	return de, nil
}

func (c *Client) addReaders(de *upspin.DirEntry, name upspin.PathName, packer upspin.Packer) error {
	directory, err := bind.DirServer(c.context, c.context.DirEndpoint())
	if err != nil {
		return err
	}

	// Add other readers to Packdata.
	// We do this before "Store contents", so an error return wastes little.
	accessEntry, err := directory.WhichAccess(name)
	if err != nil {
		// TODO: implement links.
		if e, ok := err.(*errors.Error); ok && e.Kind == errors.NotExist {
			// If WhichAccess returns a "not found" error then
			// either the destination directory doesn't exist or we don't
			// have permission to probe that name space.
			// Either way, we don't have permission to write here
			// so return a permission error.
			// This tweak guarantees that the error message from
			// Put is independent of the packing.
			e.Kind = errors.Permission
		}
		return err
	}
	var readers []upspin.UserName
	if accessEntry != nil {
		accessData, err := c.Get(accessEntry.Name)
		if err != nil {
			return err
		}
		acc, err := access.Parse(accessEntry.Name, accessData)
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
		// TODO: implement links.
		return nil, errors.E(op, err)
	}
	if entry.IsDir() {
		return nil, errors.E(op, name, errors.IsDir)
	}
	data, err := clientutil.ReadAll(c.context, entry)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return data, nil
}

// Lookup implements upspin.Client.
func (c *Client) Lookup(name upspin.PathName, followFinal bool) (*upspin.DirEntry, error) {
	return nil, errors.E("client.Lookup", errors.Str("not implemented"))
}

// Delete implements upspin.Client.
func (c *Client) Delete(name upspin.PathName) error {
	return errors.E("client.Delete", errors.Str("not implemented"))
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
		err = errors.Errorf("client: no DirServer endpoint for user %q", parsed.User())
	}
	return nil, err
}

// PutDuplicate implements upspin.Client. This is more a copy on write than a Unix style Link. As soon as
// one of the two files is written, then will diverge.
func (c *Client) PutDuplicate(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	return c.linkOrRename(oldName, newName, false)
}

// Rename implements upspin.Client.  Performed by linking to the new name and deleting the old one.
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
	if _, err := newDir.Put(entry); err != nil {
		// TODO: Implement links.
		return nil, err
	}

	if rename {
		// Remove original entry.
		if _, err := oldDir.Delete(oldName); err != nil {
			// TODO: Implement links.
			return entry, err
		}
	}
	return entry, nil
}
