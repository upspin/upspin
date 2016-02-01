// Package pack provides the registry for implementations of Packing algorithms.
package pack

import (
	"fmt"

	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	packers = make(map[upspin.Packing]upspin.Packer)
	inited  = false
)

func init() {
	inited = true
}

// Register binds a Packing code to the implementation of its algorithm.
// It must be called in the init function of a Packer implementation.
// If called after the program is initialized, Register will panic.
// If multiple calls have the same Packing, Register will panic.
// TODO: One day, or in other languages, we may be able to bind lazily.
func Register(packing upspin.Packing, packer upspin.Packer) error {
	if inited {
		// Too late.
		panic(fmt.Sprintf("pack: Register(%d=%q) called after init", packing, packer))
	}
	if p, present := packers[packing]; present {
		panic(fmt.Sprintf("pack: Register(%d) already installed as %q", packing, p))
	}
	packers[packing] = packer
	return nil
}

// Lookup returns the implementation of the specified Packing, or nil if none is registered.
func Lookup(p upspin.Packing) upspin.Packer {
	return packers[p]
}
