// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"fmt"
	"net"
	"strings"

	"upspin.io/upspin"
)

const localSuffix = ".localhost."

// LocalName constructs the host local name for a service.
func LocalName(config upspin.Config, service string) string {
	s := fmt.Sprintf("%s.%s%s", config.UserName(), service, localSuffix)
	return strings.Replace(s, "@", ".", 1)
}

// IsLocal returns true if the address is host local.
func IsLocal(address string) bool {
	h, _, err := net.SplitHostPort(address)
	if err != nil {
		h = address
	}
	if !strings.HasSuffix(h, localSuffix) {
		return false
	}
	return true
}
