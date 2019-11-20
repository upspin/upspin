// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build debug

package errors_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/valid"
)

var errorLines = strings.Split(strings.TrimSpace(`
	.*/upspin.io/errors/debug_test.go:\d+: upspin.io/errors_test..*
	.*/upspin.io/errors/debug_test.go:\d+: .*
	.*/upspin.io/valid/valid.go:\d+: .*valid.UserName:
	.*/upspin.io/user/user.go:\d+: ...user.Parse: op: user@home/path: invalid operation:
	valid.UserName:
	user.Parse: user bad-username: user name must contain one @ symbol
`), "\n")

var errorLineREs = make([]*regexp.Regexp, len(errorLines))

func init() {
	for i, s := range errorLines {
		errorLineREs[i] = regexp.MustCompile(fmt.Sprintf("^%s$", s))
	}
}

// Test that the error stack includes all the function calls between where it
// was generated and where it was printed. It should not include the name
// of the function in which the Error method is called. It should coalesce
// the call stacks of nested errors into one single stack, and present that
// stack before the other error values.
func TestDebug(t *testing.T) {
	got := printErr(t, func1())
	lines := strings.Split(got, "\n")
	for i, re := range errorLineREs {
		if i >= len(lines) {
			// Handled by line number check.
			break
		}
		if !re.MatchString(lines[i]) {
			t.Errorf("error does not match at line %v, got:\n\t%q\nwant:\n\t%q", i, lines[i], re)
		}
	}
	// Check number of lines after checking the lines themselves,
	// as the content check will likely be more illuminating.
	if got, want := len(lines), len(errorLines); got != want {
		t.Errorf("got %v lines of errors, want %v", got, want)
	}
}

func printErr(t *testing.T, err error) string {
	return err.Error()
}

func func1() error {
	var t T
	return t.func2()
}

type T struct{}

func (T) func2() error {
	return errors.E(errors.Op("op"), upspin.PathName("user@home/path"), func3())
}

func func3() error {
	return func4()
}

func func4() error {
	return valid.UserName("bad-username")
}
