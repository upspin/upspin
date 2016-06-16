// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"syscall"

	"bazil.org/fuse"

	"upspin.io/errors"
	"upspin.io/log"
)

// errnoError is a go string with a POSIX syscall error number.
type errnoError struct {
	errno syscall.Errno
	err   error
}

func (u *errnoError) Error() string {
	return u.err.Error()
}

func (u *errnoError) Errno() fuse.Errno {
	return fuse.Errno(u.errno)
}

var errs = []struct {
	str   string
	errno syscall.Errno
}{
	{"not found", syscall.ENOENT},
	{"not a directory", syscall.ENOTDIR},
	{"no such", syscall.ENOENT},
	{"permission", syscall.EPERM},
	{"not empty", syscall.ENOTEMPTY},
}

// e2e converts an upspin error into a fuse one.
func e2e(err error) *errnoError {
	errno := syscall.EIO
	if ue, ok := err.(*errors.Error); ok {
		switch ue.Kind {
		case errors.Permission:
			errno = syscall.EPERM
		case errors.Exist:
			errno = syscall.EEXIST
		case errors.NotExist:
			errno = syscall.ENOENT
		case errors.Syntax:
			errno = syscall.ENOENT
		case errors.IsDir:
			errno = syscall.EISDIR
		case errors.NotDir:
			errno = syscall.ENOTDIR
		case errors.NotEmpty:
			errno = syscall.ENOTEMPTY
		}
	} else {
		for _, e := range errs {
			if strings.Contains(err.Error(), e.str) {
				errno = e.errno
				break
			}
		}
	}
	log.Debug.Println(err.Error())
	return &errnoError{errno, err}
}
