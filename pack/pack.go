// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pack provides the registry for implementations of Packing algorithms.
package pack // import "upspin.io/pack"

import (
	"fmt"
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var (
	packers = make(map[upspin.Packing]upspin.Packer)
	mu      sync.Mutex
)

// Register binds a Packing code to the implementation of its algorithm.
// It must be called in the init function of a Packer implementation.
// If multiple calls have the same Packing, Register will panic.
// TODO: One day, or in other languages, we may be able to bind lazily.
func Register(packer upspin.Packer) error {
	packing := packer.Packing()
	if packing == upspin.UnassignedPack {
		return errors.E(errors.Invalid, "unassigned pack cannot be registered")
	}
	mu.Lock()
	defer mu.Unlock()
	if p, present := packers[packer.Packing()]; present {
		panic(fmt.Sprintf("pack: Register(%d) already installed as %q", p.Packing(), p))
	}
	packers[packing] = packer
	return nil
}

// Lookup returns the implementation of the specified Packing, or nil if none is registered.
func Lookup(p upspin.Packing) upspin.Packer {
	mu.Lock()
	packer := packers[p]
	mu.Unlock() // Not worth a defer.
	return packer
}

// LookupByName returns the implementation of the specified Packing, or nil if none is registered.
func LookupByName(name string) upspin.Packer {
	mu.Lock()
	defer mu.Unlock()
	for _, packer := range packers {
		if packer.String() == name {
			return packer
		}
	}
	return nil
}

var (
	// ErrBadPacking indicates that the packing code is invalid.
	ErrBadPacking = errors.Str("DirEntry has incorrect Packing value")
)

// CheckPacking verifies that the DirEntry matches the packing type for Pack and Packlen.
func CheckPacking(p upspin.Packer, entry *upspin.DirEntry) error {
	if entry.Packing != p.Packing() {
		return ErrBadPacking
	}
	return nil
}
