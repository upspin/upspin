// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !debug

package errors

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"upspin.io/upspin"
)

func TestDebug(t *testing.T) {
	// Test with -tags debug to run the tests in debug_test.go
	cmd := exec.Command("go", "test", "-tags", "debug")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("external go test failed: %v", err)
	}
}

func TestMarshal(t *testing.T) {
	path := upspin.PathName("jane@doe.com/file")
	user := upspin.UserName("joe@blow.com")

	// Single error. No user is set, so we will have a zero-length field inside.
	e1 := E(Op("Get"), path, IO, "network unreachable")

	// Nested error.
	e2 := E(Op("Read"), path, user, Other, e1)

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

	// Single error. No user is set, so we will have a zero-length field inside.
	e1 := E(Op("Get"), path, IO, "network unreachable")

	// Nested error.
	e2 := E(Op("Read"), path, user, Other, e1)

	want := "Read: jane@doe.com/file, user joe@blow.com: I/O error:: Get: network unreachable"
	if errorAsString(e2) != want {
		t.Errorf("expected %q; got %q", want, e2)
	}
}

func TestDoesNotChangePreviousError(t *testing.T) {
	err := E(Permission)
	err2 := E(Op("I will NOT modify err"), err)

	expected := "I will NOT modify err: permission denied"
	if errorAsString(err2) != expected {
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

const (
	op  = Op("Op")
	op1 = Op("Op1")
	op2 = Op("Op2")
)

var matchTests = []matchTest{
	// Errors not of type *Error fail outright.
	{nil, nil, false},
	{io.EOF, io.EOF, false},
	{E(io.EOF), io.EOF, false},
	{io.EOF, E(io.EOF), false},
	// Success. We can drop fields from the first argument and still match.
	{E(io.EOF), E(io.EOF), true},
	{E(op, Invalid, io.EOF, jane, path1), E(op, Invalid, io.EOF, jane, path1), true},
	{E(op, Invalid, io.EOF, jane), E(op, Invalid, io.EOF, jane, path1), true},
	{E(op, Invalid, io.EOF), E(op, Invalid, io.EOF, jane, path1), true},
	{E(op, Invalid), E(op, Invalid, io.EOF, jane, path1), true},
	{E(op), E(op, Invalid, io.EOF, jane, path1), true},
	// Failure.
	{E(io.EOF), E(io.ErrClosedPipe), false},
	{E(op1), E(op2), false},
	{E(Invalid), E(Permission), false},
	{E(jane), E(john), false},
	{E(path1), E(path2), false},
	{E(op, Invalid, io.EOF, jane, path1), E(op, Invalid, io.EOF, john, path1), false},
	{E(path1, Str("something")), E(path1), false}, // Test nil error on rhs.
	// Nested *Errors.
	{E(op1, E(path1)), E(op1, john, E(op2, jane, path1)), true},
	{E(op1, path1), E(op1, john, E(op2, jane, path1)), false},
	{E(op1, E(path1)), E(op1, john, Str(E(op2, jane, path1).Error())), false},
}

func TestMatch(t *testing.T) {
	for _, test := range matchTests {
		matched := Match(test.err1, test.err2)
		if matched != test.matched {
			t.Errorf("Match(%q, %q)=%t; want %t", test.err1, test.err2, matched, test.matched)
		}
	}
}

type kindTest struct {
	err  error
	kind Kind
	want bool
}

var kindTests = []kindTest{
	// Non-Error errors.
	{nil, NotExist, false},
	{Str("not an *Error"), NotExist, false},

	// Basic comparisons.
	{E(NotExist), NotExist, true},
	{E(Exist), NotExist, false},
	{E("no kind"), NotExist, false},
	{E("no kind"), Other, false},

	// Nested *Error values.
	{E("Nesting", E(NotExist)), NotExist, true},
	{E("Nesting", E(Exist)), NotExist, false},
	{E("Nesting", E("no kind")), NotExist, false},
	{E("Nesting", E("no kind")), Other, false},
}

func TestKind(t *testing.T) {
	for _, test := range kindTests {
		got := Is(test.kind, test.err)
		if got != test.want {
			t.Errorf("Is(%q, %q)=%t; want %t", test.kind, test.err, got, test.want)
		}
	}
}

// errorAsString returns the string form of the provided error value.
// If the given string is an *Error, the stack information is removed
// before the value is stringified.
func errorAsString(err error) string {
	if e, ok := err.(*Error); ok {
		e2 := *e
		e2.stack = stack{}
		return e2.Error()
	}
	return err.Error()
}

// Simple test for issue 398.
func TestIssue398(t *testing.T) {
	e := E("a@b.com", "c@d.com", "e@f.com/", "g@h.com/").(*Error)
	// First should win.
	if e.User != "a@b.com" {
		t.Errorf("wrong user: got %q; want %q", e.User, "a@b.com")
	}
	if e.Path != "e@f.com/" {
		t.Errorf("wrong path:  got %q; want %q", e.Path, "e@f.com/")
	}
}
