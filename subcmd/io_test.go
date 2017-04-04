// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package subcmd

import (
	"fmt"
	"os/user"
	"testing"
)

func testingUserLookup(who string) (*user.User, error) {
	switch who {
	case "":
		return &user.User{
			HomeDir: "/usr/default",
		}, nil
	case "ann":
		return &user.User{
			HomeDir: "/usr/ann",
		}, nil
	}
	return nil, fmt.Errorf("no such user")
}

var tildeTests = []struct{ in, out string }{
	{"", ""},
	{"~", "/usr/default"},
	{"~/", "/usr/default"},
	{"~/x", "/usr/default/x"},
	{"~ann", "/usr/ann"},
	{"~ann/", "/usr/ann"},
	{"~ann/x", "/usr/ann/x"},
	{"~xxx", "~xxx"},
	{"~xxx/", "~xxx"},
	{"~xxx/x", "~xxx/x"},
}

func TestTilde(t *testing.T) {
	userLookup = testingUserLookup
	defer func() {
		userLookup = user.Lookup
	}()
	for _, test := range tildeTests {
		out := Tilde(test.in)
		if out != test.out {
			t.Errorf("Tilde(%q) = %q; expected %q", test.in, out, test.out)
		}
	}
}
