// Package proto contains the protocol buffer definitions shared between RPC servers and clients,
// mirroring the interfaces and types in the upspin package itself.
package proto

import "upspin.io/upspin"

// To regenerate the protocol buffer output for this package, run
//	go generate

//go:generate protoc upspin.proto --go_out=plugins=grpc:.

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

// UpspinEndpoints converts from slices of proto's Endpoint struct to upspin's.
// An unfortunate side-effect of not letting protobufs rule our types.
func UpspinEndpoints(e []*Endpoint) []upspin.Endpoint {
	if len(e) == 0 {
		return nil
	}
	ueps := make([]upspin.Endpoint, len(e))
	for i := range ueps {
		ep := e[i]
		ueps[i] = upspin.Endpoint{
			Transport: upspin.Transport(ep.Transport),
			NetAddr:   upspin.NetAddr(ep.NetAddr),
		}
	}
	return ueps
}

// Endpoints converts from slices of upspin's Endpoint struct to proto's.
// An unfortunate side-effect of not letting protobufs rule our types.
func Endpoints(ue []upspin.Endpoint) []*Endpoint {
	if len(ue) == 0 {
		return nil
	}
	eps := make([]*Endpoint, len(ue))
	for i := range eps {
		ep := ue[i]
		eps[i] = &Endpoint{
			Transport: int32(ep.Transport),
			NetAddr:   string(ep.NetAddr),
		}
	}
	return eps
}

// UpspinPublicKeys converts from slices of strings to upspin's PublicKeys.
// An unfortunate side-effect of not letting protobufs rule our types.
func UpspinPublicKeys(s []string) []upspin.PublicKey {
	if len(s) == 0 {
		return nil
	}
	upk := make([]upspin.PublicKey, len(s))
	for i := range upk {
		upk[i] = upspin.PublicKey(s[i])
	}
	return upk
}

// PublicKeys converts from slices of upspin's PublicKey to string.
// An unfortunate side-effect of not letting protobufs rule our types.
func PublicKeys(upk []upspin.PublicKey) []string {
	if len(upk) == 0 {
		return nil
	}
	s := make([]string, len(upk))
	for i := range s {
		s[i] = string(upk[i])
	}
	return s
}
