// Package proto contains the definitions shared between RPC store server and client,
// one pair for each remote call.
package proto

import "upspin.googlesource.com/upspin.git/upspin"

// To regenerate the protocol buffer output for this package, run
//	go generate

//go:generate protoc store.proto --go_out=plugins=grpc:.

// UpspinLocations converts from slices of proto's Location struct to upspin's.
// An unfortunate side-effect of not letting protobufs rule our types.
func UpspinLocations(l []*Location) []upspin.Location {
	if len(l) == 0 {
		return nil
	}
	ulocs := make([]upspin.Location, len(l))
	for i := range ulocs {
		loc := l[i]
		ulocs[i] = upspin.Location{
			Endpoint: upspin.Endpoint{
				Transport: upspin.Transport(loc.Endpoint.Transport),
				NetAddr:   upspin.NetAddr(loc.Endpoint.NetAddr),
			},
			Reference: upspin.Reference(loc.Reference),
		}
	}
	return ulocs
}

// Locations converts from slices of upspin's Location struct to proto's.
// An unfortunate side-effect of not letting protobufs rule our types.
func Locations(ul []upspin.Location) []*Location {
	if len(ul) == 0 {
		return nil
	}
	locs := make([]*Location, len(ul))
	for i := range locs {
		loc := ul[i]
		locs[i] = &Location{
			Endpoint: &Endpoint{
				Transport: int32(loc.Endpoint.Transport),
				NetAddr:   string(loc.Endpoint.NetAddr),
			},
			Reference: string(loc.Reference),
		}
	}
	return locs
}
