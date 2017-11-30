// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// PublicUserKeyService returns a Lookup function that looks up user's public keys.
// The lookup function returned is bound to a well-known public Upspin user service.
func PublicUserKeyService(cfg upspin.Config) func(userName upspin.UserName) (upspin.PublicKey, error) {
	const op errors.Op = "rpc.PublicUserKeyService"
	return func(userName upspin.UserName) (upspin.PublicKey, error) {
		key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
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
