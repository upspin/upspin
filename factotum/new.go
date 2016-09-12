// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !mobile

package factotum

import (
	"fmt"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// New returns a new Factotum providing all needed private key operations,
// loading keys from a directory (which looks for dir/*.upspinkey files).
// Our desired end state is that Factotum is implemented on each platform by the
// best local means of protecting private keys.  Please do not break the abstraction
// by hand coding direct generation or use of private keys.
func New(dir string) (upspin.Factotum, error) {
	const op = "factotum.New(dir)"
	privBytes, err := readFile(op, dir, "secret.upspinkey")
	if err != nil {
		return nil, errors.E(op, err)
	}
	privBytes = stripCR(privBytes)
	pubBytes, err := readFile(op, dir, "public.upspinkey")
	if err != nil {
		return nil, errors.E(op, err)
	}
	pubBytes = stripCR(pubBytes)

	// Read older key pairs.
	s2, err := readFile(op, dir, "secret2.upspinkey")
	if err != nil && !errors.Match(errors.E(errors.NotExist), err) {
		return nil, err
	}
	s2 = stripCR(s2)

	return newFactotum(fmt.Sprintf("%s(%q)", op, dir), pubBytes, privBytes, s2)
}
