package main

import (
	"fmt"
	"log"
	"syscall"

	"bazil.org/fuse"
)

// upspinError is an error string with a POSIX syscall error number.
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

func mkError(errno syscall.Errno, format string, vars ...interface{}) *upspinError {
	msg := fmt.Sprintf(format, vars...)
	log.Println(msg)
	return &upspinError{errno, msg}
}

func enoent(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.ENOENT, "No such file or directory: "+format, vars...)
}
func eio(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.EIO, format, vars...)
}
func eperm(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.EPERM, "Operation not permitted: "+format, vars...)
}
func eexist(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.EEXIST, "File exists: "+format, vars...)
}
func enotsup(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.ENOTSUP, "Operation not supported: "+format, vars...)
}
func enotdir(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.ENOTSUP, "Not a directory: "+format, vars...)
}
func eisdir(format string, vars ...interface{}) *upspinError {
	return mkError(syscall.EISDIR, "Is a directory: "+format, vars...)
}

// TODO(p): make this mac specific or remove it when we're done debugging.
func edotunderscore() *upspinError {
	return &upspinError{syscall.ENOENT, ""}
}
