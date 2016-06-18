// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errors_test

import (
	"fmt"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func ExampleError() {
	path := upspin.PathName("jane@doe.com/file")
	user := upspin.UserName("joe@blow.com")
	err := errors.Errorf("network unreachable")

	// Single error.
	e1 := errors.E(path, "Get", errors.IO, err)
	fmt.Println("\nSimple error:")
	fmt.Println(e1)

	// Nested error.
	fmt.Println("\nNested error:")
	e2 := errors.E(path, user, "Read", errors.Other, e1)
	fmt.Println(e2)

	// Output:
	//
	// Simple error:
	// jane@doe.com/file: Get: I/O error: network unreachable
	//
	// Nested error:
	// jane@doe.com/file, user joe@blow.com: Read:
	//	jane@doe.com/file: Get: I/O error: network unreachable
}
