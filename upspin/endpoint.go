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
	case "https":
		if len(elems) < 2 {
			return nil, fmt.Errorf("https endpoint %q requires a netaddr", v)
		}
		return &Endpoint{Transport: HTTPS, NetAddr: NetAddr(elems[1])}, nil
	case "unassigned":
		return &Endpoint{Transport: Unassigned}, nil
	}

	return nil, fmt.Errorf("unknown transport type in endpoint %q", v)
}

// toString converts an endpoint to a string.
func (ep Endpoint) toString() (string, error) {
	switch ep.Transport {
	case GCP:
		return fmt.Sprintf("gcp,%s", string(ep.NetAddr)), nil
	case InProcess:
		return "inprocess", nil
	case Remote:
		return fmt.Sprintf("remote,%s", string(ep.NetAddr)), nil
	case HTTPS:
		return fmt.Sprintf("https,%s", string(ep.NetAddr)), nil
	case Unassigned:
		return "unassigned", nil
	}
	// Note: can't use errors here.
	return "", fmt.Errorf("unknown endpoint {%v, %v}", ep.Transport, ep.NetAddr)
}

// String converts an endpoint to a string.
// TODO: possibly remove this everywhere if MarshalJSON is enough.
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
	return json.Marshal(str)
}

// UnmarshalJSON implements json.Unmarshaler.
func (ep *Endpoint) UnmarshalJSON(data []byte) error {
	var str string
	err := json.Unmarshal(data, &str)
	if err != nil {
		return err
	}
	fmt.Printf("Got str: %q from data: %q", str, data)
	newEndPt, err := ParseEndpoint(str)
	if err != nil {
		return err
	}
	*ep = *newEndPt
	return nil
}
