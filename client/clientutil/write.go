// Copyright 2018 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package clientutil

import (
	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

type GetFn func(name upspin.PathName) ([]byte, error)

func WrapKeys(cfg upspin.Config, get GetFn, entry, accessEntry *upspin.DirEntry) error {
	const op = errors.Op("clientutil.WrapKeys")

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		return errors.E(op, entry.Name, errors.Invalid, errors.Errorf("unrecognized Packing %d", entry.Packing))
	}
	readers, err := GetReaders(cfg, get, entry.Name, accessEntry)
	if err != nil {
		return errors.E(op, err)
	}
	if err := AddReaders(cfg, entry, packer, readers); err != nil {
		return errors.E(op, err)
	}
	return nil
}

// getReaders returns the list of intended readers for the given name
// according to the Access file.
// If the Access file cannot be read because of lack of permissions,
// it returns the owner of the file (but only if we are not the owner).
func GetReaders(cfg upspin.Config, get GetFn, name upspin.PathName, accessEntry *upspin.DirEntry) ([]upspin.UserName, error) {
	const op = errors.Op("clientutil.GetReaders")

	if accessEntry == nil {
		// No Access file present, therefore we must be the owner.
		return nil, nil
	}
	accessData, err := get(accessEntry.Name)
	if errors.Is(errors.NotExist, err) || errors.Is(errors.Permission, err) || errors.Is(errors.Private, err) {
		// If we failed to get the Access file for access-control
		// reasons, then we must not have read access and thus
		// cannot know the list of readers.
		// Instead, just return the owner as the only reader.
		parsed, err := path.Parse(name)
		if err != nil {
			return nil, errors.E(op, err)
		}
		owner := parsed.User()
		if owner == cfg.UserName() {
			// We are the owner, but the caller always
			// adds the us, so return an empty list.
			return nil, nil
		}
		return []upspin.UserName{owner}, nil
	} else if err != nil {
		// We failed to fetch the Access file for some unexpected reason,
		// so bubble the error up.
		return nil, errors.E(op, err)
	}
	acc, err := access.Parse(accessEntry.Name, accessData)
	if err != nil {
		return nil, errors.E(op, err)
	}
	readers, err := acc.Users(access.Read, get)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return readers, nil
}

// For EE, update the packing for the other readers as specified by the Access file.
// This call, if successful, will replace entry.Name with the value, after any
// link evaluation, from the final call to WhichAccess. The caller may then
// use that name or entry to avoid evaluating the links again.
func AddReaders(cfg upspin.Config, entry *upspin.DirEntry, packer upspin.Packer, readers []upspin.UserName) error {
	const op = errors.Op("clientutil.AddReaders")

	if packer.Packing() != upspin.EEPack {
		return nil
	}

	name := entry.Name

	// Add other readers to Packdata.
	readersPublicKey := make([]upspin.PublicKey, 0, len(readers)+2)
	f := cfg.Factotum()
	if f == nil {
		return errors.E(op, name, errors.Permission, "no factotum available")
	}
	readersPublicKey = append(readersPublicKey, f.PublicKey())
	all := access.IsAccessControlFile(entry.Name)
	for _, r := range readers {
		if r == access.AllUsers {
			all = true
			continue
		}
		key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
		if err != nil {
			return errors.E(op, err)
		}
		u, err := key.Lookup(r)
		if err != nil || len(u.PublicKey) == 0 {
			// TODO warn that we can't process one of the readers?
			continue
		}
		if u.PublicKey != readersPublicKey[0] { // don't duplicate self
			// TODO(ehg) maybe should check for other duplicates?
			readersPublicKey = append(readersPublicKey, u.PublicKey)
		}
	}
	if all {
		readersPublicKey = append(readersPublicKey, upspin.AllUsersKey)
	}

	packdata := make([]*[]byte, 1)
	packdata[0] = &entry.Packdata
	packer.Share(cfg, readersPublicKey, packdata)
	return nil
}
