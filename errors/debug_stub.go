// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !debug

package errors

import "bytes"

// These are stubs that disable stack collecting/printing
// when the debug build tag is not set.
// For documentation about these types and functions, see debug.go.

type stack struct{}

func (e *Error) populateStack()           {}
func (e *Error) printStack(*bytes.Buffer) {}
