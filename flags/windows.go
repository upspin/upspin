// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package flags

import (
	"os"
)

// homedir returns the value of the USERPROFILE environment variable on Windows.
func homedir() string {
	return os.Getenv("USERPROFILE")
}
