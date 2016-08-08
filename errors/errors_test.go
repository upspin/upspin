// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errors

import (
	"io"
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

func TestDoesNotChangePreviousError(t *testing.T) {
	err := E(Permission)
	err2 := E("I will NOT modify err", err)

	expected := "I will NOT modify err: permission denied"
	if err2.Error() != expected {
		t.Fatalf("Expected %q, got %q", expected, err2)
	}
	kind := err.(*Error).Kind
	if kind != Permission {
		t.Fatalf("Expected kind %v, got %v", Permission, kind)
	}
}

func TestNoArgs(t *testing.T) {
	defer func() {
		err := recover()
		if err == nil {
			t.Fatal("E() did not panic")
		}
	}()
	_ = E()
}

type matchTest struct {
	err1, err2 error
	matched    bool
}

const (
	path1 = upspin.PathName("john@doe.io/x")
	path2 = upspin.PathName("john@doe.io/y")
	john  = upspin.UserName("john@doe.io")
	jane  = upspin.UserName("jane@doe.io")
)

var matchTests = []matchTest{
	// Errors not of type *Error fail outright.
	{nil, nil, false},
	{io.EOF, io.EOF, false},
	{E(io.EOF), io.EOF, false},
	{io.EOF, E(io.EOF), false},
	// Success. We can drop fields from the first argument and still match.
	{E(io.EOF), E(io.EOF), true},
	{E("Op", Syntax, io.EOF, jane, path1), E("Op", Syntax, io.EOF, jane, path1), true},
	{E("Op", Syntax, io.EOF, jane), E("Op", Syntax, io.EOF, jane, path1), true},
	{E("Op", Syntax, io.EOF), E("Op", Syntax, io.EOF, jane, path1), true},
	{E("Op", Syntax), E("Op", Syntax, io.EOF, jane, path1), true},
	{E("Op"), E("Op", Syntax, io.EOF, jane, path1), true},
	// Failure.
	{E(io.EOF), E(io.ErrClosedPipe), false},
	{E("Op1"), E("Op2"), false},
	{E(Syntax), E(Permission), false},
	{E(jane), E(john), false},
	{E(path1), E(path2), false},
	{E("Op", Syntax, io.EOF, jane, path1), E("Op", Syntax, io.EOF, john, path1), false},
	{E(path1, Str("something")), E(path1), false}, // Test nil error on rhs.
}

func TestMatch(t *testing.T) {
	for _, test := range matchTests {
		matched := Match(test.err1, test.err2)
		if matched != test.matched {
			t.Errorf("Match(%q, %q)=%t; want %t", test.err1, test.err2, matched, test.matched)
		}
	}
}
