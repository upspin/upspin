// Package endpoint contains parsing and formatting of endpoints.
package endpoint

import (
	"fmt"
	"strings"

	"upspin.googlesource.com/upspin.git/upspin"
)

// Parse a string representation into an endpoint.
func Parse(v string) (*upspin.Endpoint, error) {
	elems := strings.SplitN(v, ",", 2)
	switch elems[0] {
	case "gcp":
		if len(elems) < 2 {
			return nil, fmt.Errorf("gcp endpoint %q requires a netaddr", v)
		}
		return &upspin.Endpoint{Transport: upspin.GCP, NetAddr: upspin.NetAddr(elems[1])}, nil
	case "inprocess":
		return &upspin.Endpoint{Transport: upspin.InProcess, NetAddr: upspin.NetAddr("")}, nil

	}
	return nil, fmt.Errorf("unknown transport type in endpoint %q", v)
}

// EndpointString converts an endpoint to a string.
func String(ep *upspin.Endpoint) string {
	switch ep.Transport {
	case upspin.GCP:
		return fmt.Sprintf("gcp,%s", string(ep.NetAddr))
	case upspin.InProcess:
		return "inprocess"
	}
	return fmt.Sprintf("%v", ep)
}
