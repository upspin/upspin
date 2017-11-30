// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import (
	"upspin.io/errors"
	"upspin.io/upspin"
)

// CheckLocationSet checks whether the previous block's Location field
// was set, to prevent misuse of the BlockPacker API.
func CheckLocationSet(d *upspin.DirEntry) error {
	const op errors.Op = "pack.CheckLocationSet"
	if bs := d.Blocks; len(bs) > 0 {
		if i := len(bs) - 1; bs[i].Location == (upspin.Location{}) {
			return errors.E(op, d.Name, errors.Errorf("location not set for block %v", i))
		}
	}
	return nil
}
