// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"upspin.io/errors"
	"upspin.io/rpc/local"
	"upspin.io/upspin"
)

func CacheEndpoint(cfg upspin.Config) (*upspin.Endpoint, error) {
	const op = "rpc.CacheEndpoint"

	v := cfg.Value("cache")
	if v == "" {
		return nil, nil
	}
	// TODO(adg): do we need to parse a potential endpoint here? There was
	// a TODO(p) in package config to phase out cacheserver endpoints.
	switch v {
	case "y", "yes", "true":
		name := "remote," + local.LocalName(cfg, "cacheserver") + ":443"
		ep, err := upspin.ParseEndpoint(name)
		if err != nil {
			return nil, errors.E(op, errors.Internal, err)
		}
		return ep, nil
	default:
		return nil, nil
	}
}
