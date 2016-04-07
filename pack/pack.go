// Package pack provides the registry for implementations of Packing algorithms.
package pack

import (
	"errors"
	"fmt"
	"sync"

	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	packers = make(map[upspin.Packing]upspin.Packer)
	mu      sync.Mutex
)

// Register binds a Packing code to the implementation of its algorithm.
// It must be called in the init function of a Packer implementation.
// If called after the program is initialized, Register will panic.
// If multiple calls have the same Packing, Register will panic.
// TODO: One day, or in other languages, we may be able to bind lazily.
func Register(packer upspin.Packer) error {
	packing := packer.Packing()
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
	// ErrNilMetadata indicates that the metadata is nil.
	ErrNilMetadata = errors.New("nil metadata")

	// ErrBadPacking indicates that the packing code is invalid.
	ErrBadPacking = errors.New("metadata has incorrect or missing Packing value")
)

// CheckPackMeta verifies that the metadata satisfies the invariant for Pack and Packlen.
// It must not be nil, and if meta.PackData is not nil, its zeroth entry must be correct for
// the Packer.
func CheckPackMeta(p upspin.Packer, meta *upspin.Metadata) error {
	if meta == nil {
		return ErrNilMetadata
	}
	if meta.PackData != nil {
		if len(meta.PackData) == 0 || meta.PackData[0] != byte(p.Packing()) {
			return ErrBadPacking
		}
	}
	return nil
}

// CheckUnpackMeta verifies that the metadata satisfies the invariant for Pack and Packlen.
// It must not be nil, and the zeroth entry of meta.PackData must be correct for
// the Packer.
func CheckUnpackMeta(p upspin.Packer, meta *upspin.Metadata) error {
	if meta == nil {
		return ErrNilMetadata
	}
	if len(meta.PackData) == 0 || meta.PackData[0] != byte(p.Packing()) {
		return ErrBadPacking
	}
	return nil
}
