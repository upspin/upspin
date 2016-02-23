// Package pack provides the registry for implementations of Packing algorithms.
package pack

import (
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
