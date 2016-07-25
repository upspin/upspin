// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package plain is the no-op Packing that passes the data untouched.
// Metadata is not affected. The path name is not stored in the packed data.
package plain

import (
	"upspin.io/errors"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

type plainPack struct{}

var _ upspin.Packer = plainPack{}

func init() {
	pack.Register(plainPack{})
}

var errTooShort = errors.Str("destination slice too short")

func (plainPack) Packing() upspin.Packing {
	return upspin.PlainPack
}

func (plainPack) String() string {
	return "plain"
}

func (plainPack) ReaderHashes(packdata []byte) ([][]byte, error) {
	return nil, nil
}

func (plainPack) Share(context upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {
	// Nothing to do.
}

func (p plainPack) Pack(context upspin.Context, ciphertext, cleartext []byte, dirEntry *upspin.DirEntry) (int, error) {
	const Pack = "Pack"
	if len(ciphertext) < len(cleartext) {
		return 0, errors.E(Pack, errors.Invalid, dirEntry.Name, errTooShort)
	}
	return copy(ciphertext, cleartext), nil
}

func (p plainPack) Unpack(context upspin.Context, cleartext, ciphertext []byte, dirEntry *upspin.DirEntry) (int, error) {
	const Unpack = "Unpack"
	if err := pack.CheckPacking(p, dirEntry); err != nil {
		return 0, errors.E(Unpack, errors.Invalid, dirEntry.Name, err)
	}
	if len(cleartext) < len(ciphertext) {
		return 0, errors.E(Unpack, errors.Invalid, dirEntry.Name, errTooShort)
	}
	return copy(cleartext, ciphertext), nil
}

// Name implements upspin.Name.
func (p plainPack) Name(ctx upspin.Context, dirEntry *upspin.DirEntry, newName upspin.PathName) error {
	const Name = "Name"
	if dirEntry.IsDir() {
		return errors.E(Name, errors.IsDir, dirEntry.Name, "cannot rename directory")
	}
	parsed, err := path.Parse(newName)
	if err != nil {
		return errors.E(Name, err)
	}
	dirEntry.Name = parsed.Path()
	return nil
}

func (p plainPack) PackLen(context upspin.Context, cleartext []byte, entry *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, entry); err != nil {
		return -1
	}
	return len(cleartext)
}

func (p plainPack) UnpackLen(context upspin.Context, ciphertext []byte, entry *upspin.DirEntry) int {
	if err := pack.CheckPacking(p, entry); err != nil {
		return -1
	}
	return len(ciphertext)
}
