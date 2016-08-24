// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build debug

package errors_test

import (
	"regexp"
	"strings"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/valid"
)

var debugWantRE = regexp.MustCompile(strings.TrimSpace(`
	^.*/upspin.io/errors/debug_test.go:\d+: upspin.io/errors_test.func1:
	.*/upspin.io/errors/debug_test.go:\d+: ...T.func2:
	.*/upspin.io/errors/debug_test.go:\d+: ...func3:
	.*/upspin.io/errors/debug_test.go:\d+: ...func4:
	.*/upspin.io/valid/valid.go:\d+: ...valid.UserName:
	.*/upspin.io/path/path.go:\d+: ...path.UserAndDomain:
	.*/upspin.io/path/path.go:\d+: ...errUserName: user@home/path: op: syntax error:
	valid.UserName:
	user bad-username: path.UserAndDomain: user name must contain one @ symbol$
`))

// Test that the error stack includes all the function calls between where it
// was generated and where it was printed. It should not include the name
// of the function in which the Error method is called. It should coalesce
// the call stacks of nested errors into one single stack, and present that
// stack before the other error values.
func TestDebug(t *testing.T) {
	got := printErr(t, func1())
	if !debugWantRE.MatchString(got) {
		t.Errorf("error did not match. got:\n%v", got)
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
	return errors.E("op", upspin.PathName("user@home/path"), func3())
}

func func3() error {
	return func4()
}

func func4() error {
	return valid.UserName("bad-username")
}
