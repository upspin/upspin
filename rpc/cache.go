// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"strings"

	"upspin.io/errors"
	"upspin.io/rpc/local"
	"upspin.io/upspin"
)

func CacheEndpoint(cfg upspin.Config) (*upspin.Endpoint, error) {
	const op = "rpc.CacheEndpoint"

	v := cfg.Value("cache")
	switch v {
	case "", "n", "no", "false":
		return nil, nil
	case "y", "yes", "true":
		name := "remote," + local.LocalName(cfg, "cacheserver") + ":80"
		ep, err := upspin.ParseEndpoint(name)
		if err != nil {
			return nil, errors.E(op, errors.Internal, err)
		}
		return ep, nil
	default:
		if !strings.Contains(v, ",") {
			v = "remote," + v
		}
		ep, err := upspin.ParseEndpoint(v)
		if err != nil {
			return nil, errors.E(op, errors.Invalid, err)
		}
		return ep, nil
	}
}
