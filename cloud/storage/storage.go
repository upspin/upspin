// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package storage implements a low-level interface for storing blobs in
// stable storage such as a database.
package storage // import "upspin.io/cloud/storage"

import (
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// Storage is a low-level storage interface for services to store their data
// permanently. Storage implementations must be safe for concurrent use.
type Storage interface {
	// LinkBase returns the base URL from which any ref may be downloaded.
	// If the backend does not offer direct links it returns
	// upspin.ErrNotSupported.
	LinkBase() (base string, err error)

	// Download retrieves the bytes associated with a ref.
	Download(ref string) ([]byte, error)

	// Put stores the contents given as ref on the storage backend.
	Put(ref string, contents []byte) error

	// Delete permanently removes all storage space associated
	// with a ref.
	Delete(ref string) error
}

// Lister provides a mechanism to report the set of items held in a
// StoreServer. Clients can use a type assertion to verify whether
// the StoreServer implements this interface.
type Lister interface {
	// List returns a list of references contained by the storage backend.
	// The token argument is for pagination: it specifies a starting point
	// for the list. To obtain a complete list of references, pass an empty
	// string for the first call, and the last nextToken value for for each
	// subsequent call. The pagination tokens are opaque values particular
	// to the storage implementation.
	List(token string) (refs []upspin.ListRefsItem, nextToken string, err error)
}

// StorageConstructor is a function that initializes and returns a Storage
// implementation with the given options.
type StorageConstructor func(*Opts) (Storage, error)

var registration = make(map[string]StorageConstructor)

// Opts holds configuration options for the storage backend.
// It is meant to be used by implementations of Storage.
type Opts struct {
	Opts map[string]string // key-value pair
}

// DialOpts is a daisy-chaining mechanism for setting options to a backend during Dial.
type DialOpts func(*Opts) error

// Register registers a new Storage under a name. It is typically used in init functions.
func Register(name string, fn StorageConstructor) error {
	const op errors.Op = "cloud/storage.Register"
	if _, exists := registration[name]; exists {
		return errors.E(op, errors.Exist)
	}
	registration[name] = fn
	return nil
}

// WithOptions parses a string in the format "key1=value1,key2=value2,..." where keys and values
// are specific to each storage backend. Neither key nor value may contain the characters "," or "=".
// Use WithKeyValue repeatedly if these characters need to be used.
func WithOptions(options string) DialOpts {
	const op errors.Op = "cloud/storage.WithOptions"
	return func(o *Opts) error {
		pairs := strings.Split(options, ",")
		for _, p := range pairs {
			kv := strings.SplitN(p, "=", 2)
			if len(kv) != 2 {
				return errors.E(op, errors.Invalid, errors.Errorf("error parsing option %s", kv))
			}
			o.Opts[kv[0]] = kv[1]
		}
		return nil
	}
}

// WithKeyValue sets a key-value pair as option. If called multiple times with the same key, the last one wins.
func WithKeyValue(key, value string) DialOpts {
	return func(o *Opts) error {
		o.Opts[key] = value
		return nil
	}
}

// Dial dials the named storage backend using the dial options opts.
func Dial(name string, opts ...DialOpts) (Storage, error) {
	const op errors.Op = "cloud/storage.Dial"
	fn, found := registration[name]
	if !found {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("unknown storage backend type %q", name))
	}
	dOpts := &Opts{Opts: make(map[string]string)}
	var err error
	for _, o := range opts {
		if o != nil {
			err = o(dOpts)
			if err != nil {
				return nil, err
			}
		}
	}
	return fn(dOpts)
}
