// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseEndpoint parses the string representation of an endpoint.
func ParseEndpoint(v string) (*Endpoint, error) {
	elems := strings.SplitN(v, ",", 2)
	switch elems[0] {
	case "inprocess":
		return &Endpoint{Transport: InProcess}, nil
	case "remote":
		if len(elems) < 2 {
			return nil, fmt.Errorf("remote endpoint %q requires a netaddr", v)
		}
		// Assume implicit port 443 if no port specified.
		addr := elems[1]
		if !strings.Contains(addr, ":") {
			addr += ":443"
		}
		return &Endpoint{Transport: Remote, NetAddr: NetAddr(addr)}, nil
	case "https":
		if len(elems) < 2 {
			return nil, fmt.Errorf("https endpoint %q requires a netaddr", v)
		}
		return &Endpoint{Transport: HTTPS, NetAddr: NetAddr(elems[1])}, nil
	case "unassigned":
		return &Endpoint{Transport: Unassigned}, nil
	default:
		if len(elems) != 1 {
			break
		}
		// Assume "remote" transport if none specified.
		// Assume implicit port 443 if no port specified.
		addr := elems[0]
		if !strings.Contains(addr, ":") {
			addr += ":443"
		}
		return &Endpoint{Transport: Remote, NetAddr: NetAddr(addr)}, nil
	}

	return nil, fmt.Errorf("unknown transport type in endpoint %q", v)
}

// toString converts an endpoint to a string.
func (ep Endpoint) toString() (string, error) {
	switch ep.Transport {
	case Remote, HTTPS:
		return fmt.Sprintf("%v,%v", ep.Transport, ep.NetAddr), nil
	case InProcess, Unassigned:
		return ep.Transport.String(), nil
	}
	// Note: can't use errors here.
	return "", fmt.Errorf("unknown transport {%v, %v}", ep.Transport, ep.NetAddr)
}

// String converts an endpoint to a string.
func (ep Endpoint) String() string {
	str, err := ep.toString()
	if err != nil {
		return err.Error()
	}
	return str
}

// MarshalJSON implements json.Marshaler.
func (ep *Endpoint) MarshalJSON() ([]byte, error) {
	str, err := ep.toString()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(str)
	if err != nil {
		return nil, fmt.Errorf("Endpoint: %v %#v", err, ep)
	}
	return b, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (ep *Endpoint) UnmarshalJSON(data []byte) error {
	var str string
	err := json.Unmarshal(data, &str)
	if err != nil {
		return fmt.Errorf("Endpoint: %v %#v", err, ep)
	}
	p, err := ParseEndpoint(str)
	if err != nil {
		return err
	}
	*ep = *p
	return nil
}
