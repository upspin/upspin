// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package client implements a simple client service talking to services
// running anywhere (GCP, InProcess, etc).
package client

import (
	"strings"

	"upspin.io/access"
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
}

var _ upspin.Client = (*Client)(nil)

const maxBlockSize = 1024 * 1024

const (
	followFinalLink      = true
	doNotFollowFinalLink = false
)

// New creates a Client that uses the given Context to
// access the various Upspin servers.
// It uses a global user cache to reduce round trip times.
func New(context upspin.Context) upspin.Client {
	return &Client{
		context: usercache.Global(context),
	}
}

// NewWithoutCache creates a Client that uses the given Context to
// access the various Upspin servers.
// It does no caching, and should be used only by tests.
func NewWithoutCache(context upspin.Context) upspin.Client {
	return &Client{context: context}
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

// Used by PutLink etc. but not by Put itself.
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

	// Find the Access file that applies. This will also cause us to evaluate links in the path,
	// and if we do, evalEntry will contain the true file name of the Put operation we will do.
	accessEntry, evalEntry, err := c.lookup(op, &upspin.DirEntry{Name: parsed.Path()}, whichAccessLookupFn, followFinalLink)
	if err != nil {
		return nil, errors.E(op, err)
	}
	name = evalEntry.Name

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

	if err := c.pack(entry, data, packer); err != nil {
		return nil, errors.E(op, err)
	}

	if err := c.addReaders(op, entry, accessEntry, packer); err != nil {
		return nil, err
	}

	// We have evaluated links so can use DirServer.Put directly.
	dir, err := c.DirServer(name)
	if err != nil {
		return nil, errors.E(op, err)
	}

	if e, err := dir.Put(entry); err != nil {
		return e, err
	}
	return entry, nil
}

func (c *Client) pack(entry *upspin.DirEntry, data []byte, packer upspin.Packer) error {
	// Start the I/O.
	store := c.context.StoreServer()
	bp, err := packer.Pack(c.context, entry)
	if err != nil {
		return err
	}
	for len(data) > 0 {
		n := len(data)
		if n > maxBlockSize {
			n = maxBlockSize
		}
		cipher, err := bp.Pack(data[:n])
		if err != nil {
			return err
		}
		data = data[n:]
		ref, err := store.Put(cipher)
		if err != nil {
			return err
		}
		bp.SetLocation(
			upspin.Location{
				Endpoint:  c.context.StoreEndpoint(),
				Reference: ref,
			},
		)
	}
	return bp.Close()
}

func whichAccessLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return dir.WhichAccess(entry.Name)
}

// For EE, update the packing for the other readers as specified by the Access file.
// This call, if successful, will replace entry.Name with the value, after any
// link evaluation, from the final call to WhichAccess. The caller may then
// use that name or entry to avoid evaluating the links again.
func (c *Client) addReaders(op string, entry, accessEntry *upspin.DirEntry, packer upspin.Packer) error {
	if packer.Packing() != upspin.EEPack {
		return nil
	}

	name := entry.Name

	// Add other readers to Packdata.
	readers, err := c.getReaders(op, name, accessEntry)
	if err != nil {
		return errors.E(op, name, err)
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
	return nil
}

// getReaders returns the list of intended readers for the given name
// according to the Access file.
// If the Access file cannot be read because of lack of permissions,
// it returns the owner of the file (but only if we are not the owner).
func (c *Client) getReaders(op string, name upspin.PathName, accessEntry *upspin.DirEntry) ([]upspin.UserName, error) {
	if accessEntry == nil {
		// No Access file present, therefore we must be the owner.
		return nil, nil
	}
	accessData, err := c.Get(accessEntry.Name)
	if errors.Match(errors.E(errors.NotExist), err) || errors.Match(errors.E(errors.Permission), err) {
		// If we failed to get the Access file for access-control
		// reasons, then we must not have read access and thus
		// cannot know the list of readers.
		// Instead, just return the owner as the only reader.
		parsed, err := path.Parse(name)
		if err != nil {
			return nil, err
		}
		owner := parsed.User()
		if owner == c.context.UserName() {
			// We are the owner, but the caller always
			// adds the us, so return an empty list.
			return nil, nil
		}
		return []upspin.UserName{owner}, nil
	} else if err != nil {
		// We failed to fetch the Access file for some unexpected reason,
		// so bubble the error up.
		return nil, err
	}
	acc, err := access.Parse(accessEntry.Name, accessData)
	if err != nil {
		return nil, err
	}
	readers, err := acc.Users(access.Read, c.Get)
	if err != nil {
		return nil, err
	}
	return readers, nil
}

func makeDirectoryLookupFn(dir upspin.DirServer, entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return dir.MakeDirectory(entry.Name)
}

// MakeDirectory implements upspin.Client.
func (c *Client) MakeDirectory(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "client.MakeDirectory"
	entry, _, err := c.lookup(op, &upspin.DirEntry{Name: name}, makeDirectoryLookupFn, followFinalLink)
	return entry, err
}

// Get implements upspin.Client.
func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	const op = "client.Get"
	entry, _, err := c.lookup(op, &upspin.DirEntry{Name: name}, lookupLookupFn, followFinalLink)
	if err != nil {
		return nil, errors.E(op, name, err)
	}
	if entry.IsDir() {
		return nil, errors.E(op, name, errors.IsDir)
	}
	data, err := clientutil.ReadAll(c.context, entry)
	if err != nil {
		return nil, errors.E(op, name, err)
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
// DirServer operation and may return ErrFollowLink, some other error, or success.
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
	copied := false // Do we need to allocate a new entry to modify its name?
	for loop := 0; loop < upspin.MaxLinkHops; loop++ {
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
			return resultEntry, nil, errors.E(op, err)
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
	const op = "client.Glob"
	var results []*upspin.DirEntry
	var this []string
	next := []string{pattern}
	for loop := 0; loop < upspin.MaxLinkHops && len(next) > 0; loop++ {
		this, next = next, this
		next = next[:0]
		for _, pattern := range this {
			files, links, err := c.globOnePattern(pattern)
			if err != nil {
				first := len(this) == 1 && len(next) == 0
				if first || !benignGlobError(err) {
					return nil, err
				}
				continue
			}
			results = append(results, files...)
			if len(links) == 0 {
				continue
			}
			parsed, err := path.Parse(upspin.PathName(pattern))
			if err != nil { // Cannot happen, but be careful.
				return nil, err
			}
			for _, link := range links {
				// We searched for
				//	u@g.c/a/*/b
				// and have link entry with name
				//	u@g.c/a/foo
				// and target
				// 	v@x.y/d/e/f.
				// Replace the the pattern that matches the link name
				// with the link target and try that the next time:
				// 	v@x.y/d/e/f/b.
				linkName, err := path.Parse(link.Name)
				if err != nil { // Cannot happen, but be careful.
					return nil, err
				}
				tail := strings.TrimPrefix(parsed.FilePath(),
					parsed.First(linkName.NElem()).FilePath())
				newPattern := path.Join(link.Link, tail)
				next = append(next, string(newPattern))
			}
		}
	}
	if len(next) > 0 {
		// TODO: Return partial results?
		return nil, errors.E(op, upspin.PathName(pattern), errors.Str("link loop"))
	}
	results = upspin.SortDirEntries(results, true)
	return results, nil
}

// benignGlobError reports whether the provided error can be
// safely ignored as part of a multi-request glob operation.
func benignGlobError(err error) bool {
	return errors.Match(errors.E(errors.NotExist), err) ||
		errors.Match(errors.E(errors.Permission), err)

}

func (c *Client) globOnePattern(pattern string) (entries, links []*upspin.DirEntry, err error) {
	dir, err := c.DirServer(upspin.PathName(pattern))
	if err != nil {
		return nil, nil, err
	}
	entries, err = dir.Glob(pattern)
	switch err {
	case nil:
		return entries, nil, nil
	case upspin.ErrFollowLink:
		var files, links []*upspin.DirEntry
		for _, entry := range entries {
			if entry.IsLink() {
				links = append(links, entry)
			} else {
				files = append(files, entry)
			}
		}
		return files, links, nil
	default:
		return nil, nil, err
	}
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
	return c.context.DirServer(name), nil
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
		accessEntry, _, err := c.lookup(op, entry, whichAccessLookupFn, followFinalLink)
		if err != nil {
			return nil, errors.E(op, trueOldName, err)
		}
		if err := c.addReaders(op, entry, accessEntry, packer); err != nil {
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
