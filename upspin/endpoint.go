// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"fmt"
	"strings"
)

// ParseEndpoint parses the string representation of an endpoint.
func ParseEndpoint(v string) (*Endpoint, error) {
	elems := strings.SplitN(v, ",", 2)
	switch elems[0] {
	case "gcp":
		if len(elems) < 2 {
			return nil, fmt.Errorf("gcp endpoint %q requires a netaddr", v)
		}
		return &Endpoint{Transport: GCP, NetAddr: NetAddr(elems[1])}, nil
	case "inprocess":
		return &Endpoint{Transport: InProcess}, nil
	case "remote":
		if len(elems) < 2 {
			return nil, fmt.Errorf("remote endpoint %q requires a netaddr", v)
		}
		return &Endpoint{Transport: Remote, NetAddr: NetAddr(elems[1])}, nil
	case "unassigned":
		return &Endpoint{Transport: Unassigned}, nil
	}
	return nil, fmt.Errorf("unknown transport type in endpoint %q", v)
}

// String converts an endpoint to a string.
func (ep Endpoint) String() string {
	switch ep.Transport {
	case GCP:
		return fmt.Sprintf("gcp,%s", string(ep.NetAddr))
	case InProcess:
		return "inprocess"
	case Remote:
		return fmt.Sprintf("remote,%s", string(ep.NetAddr))
	case Unassigned:
		return "unassigned"
	}
	return fmt.Sprintf("%v", ep)
}
