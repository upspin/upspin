// Package ref defines types for that represent references for the simulator.
package ref

import (
	"fmt"
	"net"

	"upspin.googlesource.com/upspin.git/sim/hash"
)

// Types for API elements to make descriptions easier to understand.
type Reference struct {
	hash.Hash
}

// HintedReference attaches a location hint to a Reference.
type HintedReference struct {
	Reference
	Location // TODO: Should be a list of locations, but makes the map harder in the Service.
}

func (hr HintedReference) String() string {
	return fmt.Sprintf("%s!%s", hr.Location, hr.Reference)
}

// Location represents a service. Maybe just a net.Addr, maybe more.
type Location struct {
	Addr net.Addr
}
