package main

import (
	"fmt"
	"syscall"

	"bazil.org/fuse"
)

// upspinError's are error strings with posix syscall error numbers.

type upspinError struct {
	errno syscall.Errno
	err   string
}

func (u *upspinError) Error() string {
	return u.err
}

func (u *upspinError) Errno() fuse.Errno {
	return fuse.Errno(u.errno)
}

func enoent(name string) *upspinError {
	return &upspinError{syscall.ENOENT, fmt.Sprintf("No such file or directory: %s", name)}
}
func eio() *upspinError   { return &upspinError{syscall.EIO, "I/O error"} }
func eperm() *upspinError { return &upspinError{syscall.EPERM, "Operation not permitted"} }
func eexist(name string) *upspinError {
	return &upspinError{syscall.EEXIST, fmt.Sprintf("File exists: %s", name)}
}
func enotsup(op string) *upspinError {
	return &upspinError{syscall.ENOTSUP, fmt.Sprintf("Operation not supported: %s", op)}
}
func enotdir(name string) *upspinError {
	return &upspinError{syscall.ENOTDIR, fmt.Sprintf("Not a directory: %s", name)}
}
func eisdir(name string) *upspinError {
	return &upspinError{syscall.EISDIR, fmt.Sprintf("Is a directory: %s", name)}
}
