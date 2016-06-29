// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package endpoint contains parsing and formatting of endpoints.
package endpoint

import (
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// Parse a string representation into an endpoint.
func Parse(v string) (*upspin.Endpoint, error) {
	elems := strings.SplitN(v, ",", 2)
	switch elems[0] {
	case "gcp":
		if len(elems) < 2 {
			return nil, errors.Errorf("gcp endpoint %q requires a netaddr", v)
		}
		return &upspin.Endpoint{Transport: upspin.GCP, NetAddr: upspin.NetAddr(elems[1])}, nil
	case "inprocess":
		return &upspin.Endpoint{Transport: upspin.InProcess}, nil
	case "remote":
		if len(elems) < 2 {
			return nil, errors.Errorf("remote endpoint %q requires a netaddr", v)
		}
		return &upspin.Endpoint{Transport: upspin.Remote, NetAddr: upspin.NetAddr(elems[1])}, nil
	case "unassigned":
		return &upspin.Endpoint{Transport: upspin.Unassigned}, nil
	}
	return nil, errors.Errorf("unknown transport type in endpoint %q", v)
}
