// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package local

import (
	"context"
	"fmt"
	"net"
)

func nameToPort(name string) uint16 {
	// Jenkins' one-at-a-time hash
	var hash uint32
	for _, c := range name {
		hash += uint32(c)
		hash += hash << 10
		hash ^= hash >> 6
	}
	hash += hash << 3
	hash ^= hash >> 11
	hash += hash << 15

	// Map hash above the restricted port space.
	hash = 1024 + (hash % (1<<16 - 1024))
	return uint16(hash)
}

// DialContextLocal dials using a tcp loopback port.
func (d *Dialer) DialContextLocal(ctx context.Context, network, address string) (net.Conn, error) {
	// Use loop back interface with a port that is a hash of the address.
	return d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", nameToPort(address)))
}

// ListenLocal listens on the a tcp loopback port.
func ListenLocal(address string) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", nameToPort(address)))
}
