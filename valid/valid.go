// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package valid does validation of various data types.
// For the most part, its functions are used by servers and enforce
// stronger constraints than client code needs to follow.
package valid // import "upspin.io/valid"

import (
	"strconv"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"
)

// UserName verifies that the name is a syntactically valid user's email address.
// It also requires that the domain name be lower-cased to avoid ambiguity.
func UserName(userName upspin.UserName) error {
	const op errors.Op = "valid.UserName"
	u, _, d, err := user.Parse(userName)
	if err != nil {
		return errors.E(op, err)
	}
	if string(userName) != u+"@"+d {
		return errors.E(op, errors.Invalid, userName, "not canonically formatted")
	}
	if userName == access.AllUsers {
		return errors.E(op, errors.Invalid, userName, "reserved user name")
	}
	return nil
}

// User verifies that the User struct is valid, that is, that all its fields are syntactically valid.
func User(user *upspin.User) error {
	const op errors.Op = "valid.User"
	if err := UserName(user.Name); err != nil {
		return errors.E(op, errors.Invalid, err)
	}
	for _, ep := range user.Dirs {
		if err := Endpoint(ep); err != nil {
			return errors.E(op, errors.Invalid, err)
		}
	}
	for _, ep := range user.Stores {
		if err := Endpoint(ep); err != nil {
			return errors.E(op, errors.Invalid, err)
		}
	}
	// TODO: Check public key?
	return nil
}

// validPathName verifies that the name is valid, clean and canonically formatted.
// See upspin.io/path.Clean for the specification. One important check is that this
// function requires a user's root to have the trailing slash; path.Parse does not.
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

// DirBlock verifies that the block is valid, that is, that it has a
// greater-than-zero Size, non-negative Offset, and valid Location.
func DirBlock(block upspin.DirBlock) error {
	const op errors.Op = "valid.DirBlock"
	if block.Size <= 0 {
		return errors.E(op, errors.Invalid, errors.Errorf("non-positive block size %d", block.Size))
	}
	if block.Offset < 0 {
		return errors.E(op, errors.Invalid, errors.Errorf("negative block offset %d", block.Offset))
	}
	if err := Endpoint(block.Location.Endpoint); err != nil {
		return errors.E(op, err)
	}
	if err := Reference(block.Location.Reference); err != nil {
		return errors.E(op, err)
	}
	return nil
}

// Endpoint verifies that the endpoint looks syntactically valid. It does not check that the
// endpoint defines a reachable server.
func Endpoint(endpoint upspin.Endpoint) error {
	const op errors.Op = "valid.Endpoint"
	switch endpoint.Transport {
	case upspin.InProcess:
		// OK if there is a netaddr, or not.
	case upspin.Unassigned:
		if endpoint.NetAddr != "" {
			return errors.E(op, errors.Invalid, errors.Errorf("%q: extraneous network address", endpoint))
		}
	case upspin.Remote:
		if endpoint.NetAddr == "" {
			return errors.E(op, errors.Invalid, errors.Errorf("%q: missing network address", endpoint))
		}
	default:
		return errors.E(op, errors.Invalid, errors.Errorf("%d unrecognized transport", endpoint.Transport))
	}
	return nil
}

// DirEntry verifies that the DirEntry is valid. It is intended for use by DirServer.Put,
// and so constrains the entry more rigorously than, for instance, the DirEntries that
// may be returned from servers. For example, it requires that a directory has no blocks,
// but a DirEntry for a directory returned by a server may contain block data.
// Rules:
// - present and valid Name and SignedName
// - Name is equal to SignedName
// - blocks may be present only if Attr == AttrNone
// - Link may be present only if Attr == AttrLink
// - Attr must not include AttrIncomplete
// - Packing must be known
// - Sequence must have a known special value or be non-negative
// - for non-directory entries, a Writer field is required.
func DirEntry(entry *upspin.DirEntry) error {
	const op errors.Op = "valid.DirEntry"
	// SignedName must be good.
	if err := validPathName(entry.SignedName); err != nil {
		return errors.E(op, errors.Invalid, entry.SignedName, err)
	}
	// Name must match.
	if entry.Name != entry.SignedName {
		return errors.E(op, errors.Invalid, entry.Name, "Name and SignedName must match")
	}
	// Is the entry incomplete? Servers must not accept such entries.
	// (Although they may return them.)
	if entry.IsIncomplete() {
		return errors.E(op, errors.Invalid, entry.Name, "entry must not be incomplete")
	}

	// Attribute must be valid and consistent with entry.
	switch entry.Attr {
	case upspin.AttrNone, upspin.AttrDirectory:
		// OK
	case upspin.AttrLink:
		if err := validPathName(entry.Link); err != nil {
			return errors.E(op, errors.Invalid, entry.Name, err)
		}
	default:
		return errors.E(op, errors.Invalid, entry.Name, errors.Errorf("invalid file attribute %d", entry.Attr))
	}

	// Blocks only for AttrNone
	if entry.Attr != upspin.AttrNone && len(entry.Blocks) > 0 {
		return errors.E(op, errors.Invalid, entry.Name, "link or directory cannot have data")
	}

	// Link only for AttrLink
	if entry.Attr != upspin.AttrLink && entry.Link != "" {
		return errors.E(op, errors.Invalid, entry.Name, "only links can have Link set")
	}

	// Packing must be valid.
	switch entry.Packing {
	case upspin.PlainPack, upspin.EEPack, upspin.EEIntegrityPack:
		// OK
	case upspin.UnassignedPack:
		if entry.IsDir() {
			// Okay for directory; DirServer chooses.
			break
		}
		fallthrough
	default:
		return errors.E(op, errors.Invalid, entry.Name, errors.Errorf("invalid packing %d", entry.Packing))
	}
	// Sequence must be valid.
	if entry.Sequence < 0 && entry.Sequence != upspin.SeqNotExist {
		return errors.E(op, errors.Invalid, entry.Name, errors.Errorf("negative sequence number %d", entry.Sequence))
	}
	// There must be no holes or overlaps in blocks and blocks must be valid.
	offset := int64(0)
	for _, block := range entry.Blocks {
		if block.Offset != offset {
			return errors.E(op, errors.Invalid, entry.Name, "data blocks are not contiguous")
		}
		offset += block.Size
		if err := DirBlock(block); err != nil {
			return errors.E(op, errors.Invalid, entry.Name, err)
		}
	}
	// For non-directory entries, a Writer field is required.
	if entry.IsDir() {
		return nil
	}
	if err := UserName(entry.Writer); err != nil {
		return errors.E(op, "invalid writer", err)
	}
	return nil
}

// Reference verifies that the Reference is valid. A Reference must be a non-empty
// UTF-8-encoded string of printable characters, as defined by Unicode. Also, although
// printable, the replacement rune (U+FFFD) is considered invalid, even if it is explicitly
// present, as it usually indicates erroneous UTF-8 or Unicode.
func Reference(ref upspin.Reference) error {
	const op errors.Op = "valid.Reference"
	if ref == "" {
		return errors.E(op, errors.Invalid, "empty reference")
	}
	previ := 0
	for i, r := range ref {
		// U+FFFD might mean invalid UTF-8, or be present for real. Either way, we reject it.
		if r == '\uFFFD' {
			if i-previ == 1 {
				return errors.E(op, errors.Invalid, errors.Errorf("invalid UTF-8 in reference"))
			}
			return errors.E(op, errors.Invalid, errors.Errorf("invalid code point %#U in reference", r))
		}
		if !strconv.IsPrint(r) {
			return errors.E(op, errors.Invalid, errors.Errorf("invalid code point %#U in reference", r))
		}
		previ = i
	}
	return nil
}
