// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errors

import (
	"testing"

	"upspin.io/upspin"
)

func TestMarshal(t *testing.T) {
	path := upspin.PathName("jane@doe.com/file")
	user := upspin.UserName("joe@blow.com")
	err := Str("network unreachable")

	// Single error. No user is set, so we will have a zero-length field inside.
	e1 := E(path, "Get", IO, err)

	// Nested error.
	e2 := E(path, user, "Read", Other, e1)

	b := MarshalError(e2)
	e3 := UnmarshalError(b)

	in := e2.(*Error)
	out := e3.(*Error)
	// Compare elementwise.
	if in.Path != out.Path {
		t.Errorf("expected Path %q; got %q", in.Path, out.Path)
	}
	if in.User != out.User {
		t.Errorf("expected User %q; got %q", in.User, out.User)
	}
	if in.Op != out.Op {
		t.Errorf("expected Op %q; got %q", in.Op, out.Op)
	}
	if in.Kind != out.Kind {
		t.Errorf("expected kind %d; got %d", in.Kind, out.Kind)
	}
	// Note that error will have lost type information, so just check its Error string.
	if in.Err.Error() != out.Err.Error() {
		t.Errorf("expected Err %q; got %q", in.Err, out.Err)
	}
}

func TestSeparator(t *testing.T) {
	defer func(prev string) {
		Separator = prev
	}(Separator)
	Separator = ":: "

	// Same pattern as above.
	path := upspin.PathName("jane@doe.com/file")
	user := upspin.UserName("joe@blow.com")
	err := Str("network unreachable")

	// Single error. No user is set, so we will have a zero-length field inside.
	e1 := E(path, "Get", IO, err)

	// Nested error.
	e2 := E(path, user, "Read", Other, e1)

	want := "jane@doe.com/file, user joe@blow.com: Read: I/O error:: Get: network unreachable"
	if e2.Error() != want {
		t.Errorf("expected %q; got %q", want, e2)
	}

}
