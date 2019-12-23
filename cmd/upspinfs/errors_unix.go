// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

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
	{"sequence number", syscall.EEXIST},
}

var errnoToKind = map[syscall.Errno]errors.Kind{
	syscall.EPERM:     errors.CannotDecrypt,
	syscall.EACCES:    errors.Permission,
	syscall.EEXIST:    errors.Exist,
	syscall.ENOENT:    errors.NotExist,
	syscall.EISDIR:    errors.IsDir,
	syscall.ENOTDIR:   errors.NotDir,
	syscall.ENOTEMPTY: errors.NotEmpty,
}

var kindToErrno = map[errors.Kind]syscall.Errno{
	errors.Permission:    syscall.EACCES,
	errors.Exist:         syscall.EEXIST,
	errors.NotExist:      syscall.ENOENT,
	errors.IsDir:         syscall.EISDIR,
	errors.NotDir:        syscall.ENOTDIR,
	errors.NotEmpty:      syscall.ENOTEMPTY,
	errors.CannotDecrypt: syscall.EPERM,
	errors.Private:       syscall.EACCES,
}

func notSupported(s string) *errnoError {
	return &errnoError{syscall.ENOSYS, errors.Str(s)}
}

// e2e converts an upspin error into a fuse one.
func e2e(err error) *errnoError {
	errno := syscall.EIO
	if ue, ok := err.(*errors.Error); ok {
		if e, ok := kindToErrno[ue.Kind]; ok {
			errno = e
		}
	}
	if errno == syscall.EIO {
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

func unsupported(err error) *errnoError {
	return &errnoError{syscall.ENOTSUP, err}
}

// classify returns the Kind of error whether or not this is from the upspin errors pkg.
func classify(err error) errors.Kind {
	if ue, ok := err.(*errors.Error); ok {
		return ue.Kind
	}
	for _, e := range errs {
		if strings.Contains(err.Error(), e.str) {
			if k, ok := errnoToKind[e.errno]; ok {
				return k
			}
			break
		}
	}
	return errors.IO
}
