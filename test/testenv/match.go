// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package testenv // import "upspin.io/test/testenv"

import "upspin.io/errors"

// errorMatch is a version of errors.Match that recursively
// checks interior errors of err2 for the fields in err1.
//
// TODO(adg): remove this entirely.
func errorMatch(err1, err2 error) bool {
	e1, ok := err1.(*errors.Error)
	if !ok {
		return false
	}
	e2, ok := err2.(*errors.Error)
	if !ok {
		return false
	}
	if e1.Path != "" && !match(e2, func(e *errors.Error) bool { return e1.Path == e.Path }) {
		return false
	}
	if e1.User != "" && !match(e2, func(e *errors.Error) bool { return e1.User == e.User }) {
		return false
	}
	if e1.Op != "" && !match(e2, func(e *errors.Error) bool { return e1.Op == e.Op }) {
		return false
	}
	if e1.Kind != errors.Other && !match(e2, func(e *errors.Error) bool { return e1.Kind == e.Kind }) {
		return false
	}
	if e1.Err != nil {
		if e2.Err == nil || e2.Err.Error() != e1.Err.Error() {
			return false
		}
	}
	return true
}

func match(err error, cmp func(*errors.Error) bool) bool {
	e, ok := err.(*errors.Error)
	if !ok {
		return false
	}
	if cmp(e) {
		return true
	}
	return match(e.Err, cmp)
}
