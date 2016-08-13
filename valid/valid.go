// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package valid does validation of various data types.
package valid

import (
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// UserName verifies that the name is a syntactically valid user's email address.
// It also requires that the name be lower-cased to avoid ambiguity.
func UserName(user upspin.UserName) error {
	const op = "valid.UserName"
	u, d, err := path.UserAndDomain(user)
	if err != nil {
		return errors.E(op, err)
	}
	if string(user) != u+"@"+d {
		return errors.E(op, user, "not canonically formatted")
	}
	return nil
}

// TODO: This is not a good check but it will serve for now.
func okUserChar(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	case '0' <= r && r <= '9':
		return true
	case r == '.' || r == '_' || r == '-':
		return true
	}
	return false
}

// User verifies that the User struct is valid.
func User(user *upspin.User) error {
	const op = "valid.User"
	if err := UserName(user.Name); err != nil {
		return errors.E(op, err)
	}
	for _, ep := range user.Dirs {
		if err := Endpoint(ep); err != nil {
			return errors.E(op, err)
		}
	}
	for _, ep := range user.Stores {
		if err := Endpoint(ep); err != nil {
			return errors.E(op, err)
		}
	}
	// TODO: Check public key?
	return nil
}

// validPathName verifies that the name is valid, clean (no redundant slashes, no
// .. elements, and so on) and canonically formatted. One important check is that
// this function requires a user's root to have the trailing slash; path.Parse does
// not.
func validPathName(name upspin.PathName) error {
	parsed, err := path.Parse(name)
	if err != nil {
		return err
	}
	if parsed.Path() != name {
		return errors.Str("name is not clean")
	}
	return nil
}

// DirBlock verifies that the block has a valid structure.
func DirBlock(block upspin.DirBlock) error {
	const op = "valid.DirBlock"
	if block.Size < 0 { // TODO: This be <= 0 but dir/inprocess creates empty blocks.
		return errors.E(op, errors.Errorf("negative block size %d", block.Size))
	}
	if block.Offset < 0 {
		return errors.E(op, errors.Errorf("negative block offset %d", block.Offset))
	}
	if err := Endpoint(block.Location.Endpoint); err != nil {
		return err
	}
	if block.Location.Reference == "" {
		return errors.E(op, errors.Str("empty reference in block"))
	}
	return nil
}

// Endpoint verifies that the endpoint looks valid.
func Endpoint(endpoint upspin.Endpoint) error {
	const op = "valid.Endpoint"
	switch endpoint.Transport {
	case upspin.InProcess, upspin.Unassigned:
		if endpoint.NetAddr != "" {
			return errors.E(op, errors.Errorf("%q: extraneous network address", endpoint))
		}
	case upspin.GCP, upspin.Remote, upspin.HTTPS:
		if endpoint.NetAddr == "" {
			return errors.E(op, errors.Errorf("%q: missing network address", endpoint))
		}
	default:
		return errors.E(op, errors.Errorf("%d unrecognized transport", endpoint.Transport))
	}
	return nil
}

// DirEntry verifies that the DirEntry is valid. It must have a valid
// name, its data must be contiguous, and so on.
func DirEntry(entry *upspin.DirEntry) error {
	const op = "valid.DirEntry"
	// Name must be good.
	if err := validPathName(entry.Name); err != nil {
		return errors.E(op, entry.Name, err)
	}
	// Attribute must be valid and consistent with entry.
	switch entry.Attr {
	case upspin.AttrNone, upspin.AttrDirectory:
		// OK
	case upspin.AttrLink:
		if len(entry.Blocks) > 0 {
			return errors.E(op, entry.Name, errors.Str("link cannot have data"))
		}
		if err := validPathName(entry.Link); err != nil {
			return errors.E(op, entry.Name, err)
		}
	default:
		return errors.E(op, entry.Name, errors.Errorf("invalid file attribute %d", entry.Attr))
	}
	// Packing must be valid.
	switch entry.Packing {
	case upspin.PlainPack, upspin.DebugPack, upspin.EEPack:
		// OK
	default:
		return errors.E(op, entry.Name, errors.Errorf("invalid packing %d", entry.Packing))
	}
	// Sequence must be valid.
	if entry.Sequence < 0 && entry.Sequence != upspin.SeqNotExist {
		return errors.E(op, entry.Name, errors.Errorf("negative sequence number %d", entry.Sequence))
	}
	// There must be no holes or overlaps in blocks and blocks must be valid.
	offset := int64(0)
	for _, block := range entry.Blocks {
		if block.Offset != offset {
			return errors.E(op, entry.Name, errors.Str("data blocks are not contiguous"))
		}
		offset += block.Size
		if err := DirBlock(block); err != nil {
			return errors.E(op, entry.Name, err)
		}
	}
	return nil
}
