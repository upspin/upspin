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
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid cache config value: %v", v))
	}
	// TODO(adg): do we need to parse a potential endpoint here? There was
	// a TODO(p) in package config to phase out cacheserver endpoints.
	switch s {
	case "y", "yes", "true":
		name := local.LocalName(cfg, "cacheserver")
		ep, err := upspin.ParseEndpoint(name)
		if err != nil {
			return nil, errors.E(op, errors.Internal, err)
		}
		return ep, nil
	default:
		return nil, nil
	}
}
