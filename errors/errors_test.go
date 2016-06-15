package errors_test

import (
	"fmt"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func ExampleError() {
	// Single error.
	path := upspin.PathName("jane@doe.com/file")
	user := upspin.UserName("joe@blow.com")
	e1 := errors.E(path, "Get", errors.IO, fmt.Errorf("network unreachable"))
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
	// jane@doe.com/file, for joe@blow.com: Read: other error:
	//	jane@doe.com/file: Get: I/O error: network unreachable
}
