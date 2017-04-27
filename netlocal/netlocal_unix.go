// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package netlocal

import (
	"context"
	"net"
	"os"
	"path"
)

// dir is the local directory unix domain sockets are to be created.
const dir = "/tmp"

// DialContextLocal dials using a unix domain socket.
func (d *Dialer) DialContextLocal(ctx context.Context, network, address string) (net.Conn, error) {
	return d.DialContext(ctx, "unix", path.Join(dir, address))
}

func ListenLocal(address string) (net.Listener, error) {
	// Ignore any Remove error. If the socket exsts we'll get an error on the Listen.
	os.Remove(address)
	return net.Listen("unix", path.Join(dir, address))
}
