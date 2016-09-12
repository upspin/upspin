// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is only compiled in mobile platforms that do not offer the notion
// of a file system directory suitable for reading keys.

// +build mobile

package factotum

import "upspin.io/upspin"

// New returns a new Factotum by providing it with the raw representation of
// an Upspin user's public, private and optionally, archived keys.
func New(public, private, archived []byte) (upspin.Factotum, error) {
	const op = "factotum.New(mobile)"
	return newFactotum(op, public, private, archived)
}
