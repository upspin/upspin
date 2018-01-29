// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package local provides interprocess communication on the local host.
package local // import "upspin.io/rpc/local"

import (
	"context"
	"net"

	"upspin.io/config"
)

type Dialer net.Dialer

// DialContext dials a service. Use it instead of the standard net.DialContext
// to use a local IPC for host names ending in localSuffix.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if config.IsLocal(address) {
		return d.DialContextLocal(ctx, network, address)
	}
	nd := net.Dialer(*d)
	return nd.DialContext(ctx, network, address)
}

// Listen listens for calls to a service. Use it instead of the standard net.Listen
// to use a local IPC for host names ending in localSuffix.
func Listen(network, address string) (net.Listener, error) {
	if config.IsLocal(address) {
		return ListenLocal(address)
	}
	return net.Listen(network, address)
}
