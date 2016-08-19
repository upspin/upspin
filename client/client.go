// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package client implements a simple client service talking to services
// running anywhere (GCP, InProcess, etc).
package client

import (
	"strings"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/client/clientutil"
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

const (
	followFinalLink      = true
	doNotFollowFinalLink = false
)

// New creates a Client. The client finds the servers according to the given Context.
func New(context upspin.Context) upspin.Client {
	return &Client{
		context: usercache.Global(context),
	}
}

// PutLink implements upspin.Client.
func (c *Client) PutLink(oldName, linkName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "client.PutLink"

	if access.IsAccessFile(oldName) || access.IsGroupFile(oldName) {
		return nil, errors.E(op, oldName, errors.Invalid, errors.Str("cannot link to Access or Group file"))
	}
	if access.IsAccessFile(linkName) || access.IsGroupFile(linkName) {
		return nil, errors.E(op, linkName, errors.Invalid, errors.Str("cannot create link named Access or Group"))
	}

	parsed, err := path.Parse(oldName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	oldName = parsed.Path() // Make sure it's clean.
	parsedLink, err := path.Parse(linkName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	linkName = parsedLink.Path() // Make sure it's clean.

	entry := &upspin.DirEntry{
		Name:     linkName,
		Packing:  upspin.PlainPack, // Unused but be explicit.
		Time:     upspin.Now(),
		Sequence: upspin.SeqIgnore,
		Writer:   c.context.UserName(),
		Link:     oldName,
		Attr:     upspin.AttrLink,
	}

	// Record directory entry.
	entry, _, err = c.lookup(op, entry, putLookupFn, doNotFollowFinalLink)
	return entry, err
}

func putLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	e, err := dir.Put(entry)
	// Put and friends must all return an entry. dir.Put doesn't, but we know
	// what it was when the call to it succeeded.
	if err != nil {
		return e, err
	}
	return entry, nil
}

// Put implements upspin.Client.
func (c *Client) Put(name upspin.PathName, data []byte) (*upspin.DirEntry, error) {
	const op = "client.Put"

	parsed, err := path.Parse(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	name = parsed.Path() // Make sure it's clean.

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

	entry := &upspin.DirEntry{
		Name:     name,
		Packing:  packer.Packing(),
		Time:     upspin.Now(),
		Sequence: upspin.SeqIgnore,
		Writer:   c.context.UserName(),
		Link:     "",
		Attr:     upspin.AttrNone,
	}

	// Start the I/O.
	store, err := bind.StoreServer(c.context, c.context.StoreEndpoint())
	if err != nil {
		return nil, err
	}
	bp, err := packer.Pack(c.context, entry)
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

	if err := c.addReaders(op, entry, entry.Name); err != nil {
		return nil, err
	}

	// Record directory entry. Its Name field may have been
	// updated by addReaders.
	entry, _, err = c.lookup(op, entry, putLookupFn, followFinalLink)
	return entry, err
}

func whichAccessLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return dir.WhichAccess(entry.Name)
}

// For EE, update the packing for the other readers as specified by the Access file.
// This call, if successful, will replace entry.Name with the value, after any
// link evaluation, from the final call to WhichAccess. The caller may then
// use that name or entry to avoid evaluating the links again.
func (c *Client) addReaders(op string, entry *upspin.DirEntry, name upspin.PathName) error {
	if entry.Packing != upspin.EEPack {
		return nil
	}
	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return errors.E(op, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}

	// Add other readers to Packdata.
	// We do this before "Store contents", so an error return wastes little.
	// evalEntry will contain the argument (most importantly the name)
	// for the last successful call to WhichAccess.
	accessEntry, evalEntry, err := c.lookup(op, &upspin.DirEntry{Name: name}, whichAccessLookupFn, followFinalLink)
	if err != nil {
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
		return errors.E(op, err)
	}
	var readers []upspin.UserName
	if accessEntry != nil {
		accessData, err := c.Get(accessEntry.Name)
		if err != nil {
			return errors.E(op, err)
		}
		acc, err := access.Parse(accessEntry.Name, accessData)
		if err != nil {
			return errors.E(op, err)
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
	packdata[0] = &entry.Packdata
	packer.Share(c.context, readersPublicKey, packdata)
	// The call to WhichAccess succeeded for evalEntry, so update
	// the incoming entry to that point, avoiding the need to
	// follow the links again.
	entry.Name = evalEntry.Name
	return nil
}

func makeDirectoryLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return dir.MakeDirectory(entry.Name)
}

// MakeDirectory implements upspin.Client.
func (c *Client) MakeDirectory(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "client.MakeDirectory"
	entry, _, err := c.lookup(op, &upspin.DirEntry{Name: name}, makeDirectoryLookupFn, doNotFollowFinalLink)
	return entry, err
}

// Get implements upspin.Client.
func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	const op = "client.Get"
	entry, _, err := c.lookup(op, &upspin.DirEntry{Name: name}, lookupLookupFn, followFinalLink)
	if err != nil {
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

func lookupLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return dir.Lookup(entry.Name)
}

// Lookup implements upspin.Client.
func (c *Client) Lookup(name upspin.PathName, followFinal bool) (*upspin.DirEntry, error) {
	const op = "client.Lookup"
	entry, _, err := c.lookup(op, &upspin.DirEntry{Name: name}, lookupLookupFn, followFinal)
	return entry, err
}

// A lookupFn is called by the evaluation loop in lookup. It calls the underlying
// DirServer operationg and may return ErrFollowLink, some other error, or success.
// If it is ErrFollowLink, lookup will step through the link and try again.
type lookupFn func(upspin.DirServer, *upspin.DirEntry) (*upspin.DirEntry, error)

// lookup returns the DirEntry referenced by the argument entry,
// evaluated by following any links in the path except maybe for one detail:
// The boolean states whether, if the final path element is a link,
// that link should be evaluated. If true, the returned entry represents
// the target of the link. If false, it represents the link itself.
//
// In some cases, such as when called from Lookup, the argument
// entry might contain nothing but a name, but it must always have a name.
// The call may overwrite the fields of the argument DirEntry,
// updating its name as it crosses links.
// The returned DirEntries on success are the result of completing
// the operation followed by the argument to the last successful
// call to fn, which for instance will contain the actual path that
// resulted in a successful call to WhichAccess.
func (c *Client) lookup(op string, entry *upspin.DirEntry, fn lookupFn, followFinal bool) (resultEntry, finalSuccessfulEntry *upspin.DirEntry, err error) {
	// As we run, we want to maintain the incoming DirEntry to track the name,
	// leaving the rest alone. As the fn will return a newly allocated entry,
	// after each link we update the entry to achieve this.
	originalName := entry.Name
	copied := false           // Do we need to allocate a new entry to modify its name?
	for i := 0; i < 10; i++ { // TODO: What is the right limit?
		parsed, err := path.Parse(entry.Name)
		if err != nil {
			return nil, nil, errors.E(op, err)
		}
		dir, err := c.DirServer(parsed.Path())
		if err != nil {
			return nil, nil, errors.E(op, err)
		}
		resultEntry, err := fn(dir, entry)
		if err == nil {
			return resultEntry, entry, nil
		}
		if err != upspin.ErrFollowLink {
			return resultEntry, nil, err
		}
		// We have a link.
		// First, allocate a new entry if necessary so we don't overwrite user's memory.
		if !copied {
			tmp := *entry
			entry = &tmp
			copied = true
		}
		// Take the prefix of the result entry and substitute that section of the existing name.
		parsedResult, err := path.Parse(resultEntry.Name)
		if err != nil {
			return nil, nil, errors.E(op, err)
		}
		resultPath := parsedResult.Path()
		// The result entry's name must be a prefix of the name we're looking up.
		if !strings.HasPrefix(parsed.String(), string(resultPath)) {
			return nil, nil, errors.E(op, resultPath, errors.Internal, errors.Str("link path not prefix"))
		}
		// Update the entry to have the new Name field.
		if resultPath == parsed.Path() {
			// We're on the last element. We may be done.
			if followFinal {
				entry.Name = resultEntry.Link
			} else {
				// Yes, we are done. Return this entry, which is a link.
				return resultEntry, entry, nil
			}
		} else {
			entry.Name = path.Join(resultEntry.Link, string(parsed.Path()[len(resultPath):]))
		}
	}
	return nil, nil, errors.E(op, errors.IO, originalName, errors.Str("link loop"))
}

func deleteLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return dir.Delete(entry.Name)
}

// Delete implements upspin.Client.
func (c *Client) Delete(name upspin.PathName) error {
	const op = "client.Delete"
	_, _, err := c.lookup(op, &upspin.DirEntry{Name: name}, deleteLookupFn, doNotFollowFinalLink)
	return err
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

// PutDuplicate implements upspin.Client.
// If one of the two files is later modified, the copy and the original will differ.
func (c *Client) PutDuplicate(oldName, newName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "client.PutDuplicate"
	return c.dupOrRename(op, oldName, newName, false)
}

// Rename implements upspin.Client.
func (c *Client) Rename(oldName, newName upspin.PathName) error {
	const op = "client.Rename"
	_, err := c.dupOrRename(op, oldName, newName, true)
	return err
}

func (c *Client) dupOrRename(op string, oldName, newName upspin.PathName, rename bool) (*upspin.DirEntry, error) {
	entry, _, err := c.lookup(op, &upspin.DirEntry{Name: oldName}, lookupLookupFn, followFinalLink)
	if err != nil {
		return nil, err
	}
	if entry.IsLink() {
		return nil, errors.E(op, oldName, errors.Internal, "after lookup, cannot be link")
	}
	if entry.IsDir() {
		return nil, errors.E(op, oldName, "cannot link or rename directories")
	}
	trueOldName := entry.Name

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return nil, errors.E(op, oldName, errors.Errorf("unrecognized Packing %d", c.context.Packing()))
	}
	if access.IsAccessFile(newName) || access.IsGroupFile(newName) {
		if entry.Packing != upspin.PlainPack {
			return nil, errors.Errorf("can only link plain packed files to access or group files")
		}
	}

	// Update the directory entry with the new name and sequence.
	// We insist the new file must not exist.
	entry.Sequence = upspin.SeqNotExist
	if err := packer.Name(c.context, entry, newName); err != nil {
		return nil, err
	}

	// Rewrap reader keys only if changing directory.
	// TODO: This could be cheaper (just compare the prefix), but it's clear and correct as written.
	newParsed, err := path.Parse(entry.Name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	oldParsed, err := path.Parse(trueOldName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if !oldParsed.Drop(1).Equal(newParsed.Drop(1)) {
		if err := c.addReaders(op, entry, entry.Name); err != nil {
			return nil, errors.E(trueOldName, err)
		}
	}

	// Record directory entry.
	entry, _, err = c.lookup(op, entry, putLookupFn, followFinalLink)
	if err != nil {
		return nil, err
	}

	if rename {
		// Remove original entry. We have all we need here and we know it's not a link.
		oldDir, err := c.DirServer(trueOldName)
		if err != nil {
			return nil, errors.E(op, err)
		}
		if _, err := oldDir.Delete(trueOldName); err != nil {
			return entry, err
		}
	}
	return entry, nil
}
