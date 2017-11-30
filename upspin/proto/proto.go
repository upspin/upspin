// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package proto contains the protocol buffer definitions shared between RPC
// servers and clients, mirroring the interfaces and types in the upspin
// package itself.
//
// These protocol buffers are used in the networking API to talk to Upspin
// servers. The wire protocol is described by package upspin.io/rpc.
//
// Unlike in some other systems, the protocol buffer types themselves are not
// used within the rest of the Upspin implementation. Instead, native Go types
// are used internally and they are converted to the protocol buffer types
// across the boundary. Helper routines in this package assist in the
// translation.
//
// Within the protocol buffers, some of the types are stored as uninterpreted
// bytes that are transcoded with custom code. For instance, the
// upspin.io/errors.Error type is transmitted as a byte slice that is marshaled
// and unmarshaled using the MarshalError and UnmarshalError routines in the
// errors package. This technique preserves the properties of the Go type across
// the network.
package proto // import "upspin.io/upspin/proto"

import (
	"time"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// To regenerate the protocol buffer output for this package, run
//	go generate

//go:generate protoc upspin.proto --go_out=.

// All these converters are an unfortunate side-effect of not letting protobufs rule our types.

// UpspinLocation converts a proto Location struct to upspin.Location.
func UpspinLocation(loc *Location) upspin.Location {
	if loc == nil {
		return upspin.Location{}
	}
	return upspin.Location{
		Endpoint: upspin.Endpoint{
			Transport: upspin.Transport(loc.Endpoint.Transport),
			NetAddr:   upspin.NetAddr(loc.Endpoint.NetAddr),
		},
		Reference: upspin.Reference(loc.Reference),
	}
}

// UpspinLocations converts from slices of proto's Location struct to upspin's.
func UpspinLocations(l []*Location) []upspin.Location {
	if len(l) == 0 {
		return nil
	}
	ulocs := make([]upspin.Location, len(l))
	for i := range ulocs {
		ulocs[i] = UpspinLocation(l[i])
	}
	return ulocs
}

// UpspinDirEntry converts a slice of bytes struct to *upspin.DirEntry.
// If the slice is nil or empty, it returns nil.
func UpspinDirEntry(b []byte) (*upspin.DirEntry, error) {
	const op errors.Op = "proto.UpspinDirEntry"
	if len(b) == 0 {
		return nil, nil
	}
	var d upspin.DirEntry
	b, err := d.Unmarshal(b)
	if err != nil {
		return nil, err
	}
	if len(b) != 0 {
		return nil, errors.E(op, errors.Invalid, "extra data")
	}
	return &d, nil
}

// Locations converts from slices of upspin's Location struct to proto's.
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

// UpspinUser converts a proto.User to upspin.User.
func UpspinUser(user *User) *upspin.User {
	return &upspin.User{
		Name:      upspin.UserName(user.Name),
		Dirs:      UpspinEndpoints(user.Dirs),
		Stores:    UpspinEndpoints(user.Stores),
		PublicKey: upspin.PublicKey(user.PublicKey),
	}
}

// UserProto converts an upspin.User to a proto.User.
func UserProto(user *upspin.User) *User {
	return &User{
		Name:      string(user.Name),
		Dirs:      Endpoints(user.Dirs),
		Stores:    Endpoints(user.Stores),
		PublicKey: string(user.PublicKey),
	}
}

// RefdataProto converts an upspin.Refdata to a proto.Refdata.
func RefdataProto(refdata *upspin.Refdata) *Refdata {
	if refdata == nil {
		return nil
	}
	return &Refdata{
		Reference: string(refdata.Reference),
		Volatile:  refdata.Volatile,
		Duration:  int64(refdata.Duration),
	}
}

// UpspinRefdata converts a proto.Refdata to upspin.Refdata.
func UpspinRefdata(refdata *Refdata) *upspin.Refdata {
	if refdata == nil {
		return nil
	}
	return &upspin.Refdata{
		Reference: upspin.Reference(refdata.Reference),
		Volatile:  refdata.Volatile,
		Duration:  time.Duration(refdata.Duration),
	}
}

// UpspinDirEntries converts from slices of bytes to upspin's *DirEntries.
func UpspinDirEntries(b [][]byte) ([]*upspin.DirEntry, error) {
	if len(b) == 0 {
		return nil, nil
	}
	ude := make([]*upspin.DirEntry, len(b))
	for i := range ude {
		var err error
		ude[i], err = UpspinDirEntry(b[i])
		if err != nil {
			return nil, err
		}
	}
	return ude, nil
}

// DirEntryBytes converts from slices of upspin's *DirEntries to bytes.
func DirEntryBytes(ude []*upspin.DirEntry) ([][]byte, error) {
	if len(ude) == 0 {
		return nil, nil
	}
	b := make([][]byte, len(ude))
	for i := range b {
		var err error
		b[i], err = ude[i].Marshal()
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

// UpspinEvent converts a proto.DirWatchResponse to upspin.Event.
func UpspinEvent(event *Event) (*upspin.Event, error) {
	entry, err := UpspinDirEntry(event.Entry)
	if err != nil {
		return nil, err
	}
	return &upspin.Event{
		Entry:  entry, // may be nil.
		Delete: event.Delete,
		Error:  errors.UnmarshalError(event.Error),
	}, nil
}

// EventProto converts an upspin.Event to proto.Event.
func EventProto(event *upspin.Event) (*Event, error) {
	if event == nil {
		// A nil proto is likely to cause GRPC to crash, according to
		// https://github.com/grpc/grpc-go/issues/532.
		// We don't use GRPC now for this protocol but we might again,
		// so play it safe.
		return &Event{}, nil
	}
	var b []byte
	if event.Entry != nil {
		var mErr error
		b, mErr = event.Entry.Marshal()
		if mErr != nil {
			return nil, mErr
		}
	}
	var err []byte
	if event.Error != nil {
		err = errors.MarshalError(event.Error)
	}
	return &Event{
		Entry:  b,
		Delete: event.Delete,
		Error:  err,
	}, nil
}
