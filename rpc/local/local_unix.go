// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

package local

import (
	"context"
	"net"
	"os"
	"path"
)

// DialContextLocal dials using a unix domain socket.
func (d *Dialer) DialContextLocal(ctx context.Context, network, address string) (net.Conn, error) {
	nd := net.Dialer(*d)
	return nd.DialContext(ctx, "unix", path.Join(os.TempDir(), address))
}

func ListenLocal(address string) (net.Listener, error) {
	// Ignore any Remove error. If the socket exsts we'll get an error on the Listen.
	fn := path.Join(os.TempDir(), address)
	os.Remove(fn)
	return net.Listen("unix", fn)
}
