// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"time"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// Config holds the configuration parameters for instantiating a server (HTTP or
// GRPC).
type Config struct {
	// Lookup looks up user keys.
	Lookup func(userName upspin.UserName) (upspin.PublicKey, error)

	// TimeFunc returns the current time. If nil, time.Now() will be used. Mostly only used for testing.
	TimeFunc func() time.Time

	// Context contains information for authenticating the server to the client (if required).
	Context upspin.Context
}

// PublicUserKeyService returns a Lookup function that looks up user's public keys.
// The lookup function returned is bound to a well-known public Upspin user service.
func PublicUserKeyService(ctx upspin.Context) func(userName upspin.UserName) (upspin.PublicKey, error) {
	const op = "auth.PublicUserKeyService"
	return func(userName upspin.UserName) (upspin.PublicKey, error) {
		key, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
		if err != nil {
			return "", errors.E(op, err)
		}
		u, err := key.Lookup(userName)
		if err != nil {
			return "", errors.E(op, err)
		}
		return u.PublicKey, nil
	}
}
