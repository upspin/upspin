// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

/*
Command upspinfs is a FUSE interface for Upspin. It presents Upspin
files as a locally mounted file system.

If the config or flags specify a cache server endpoint and cacheserver
is not running, upspinfs will attempt to start one. All the flags
listed below are also passed to the cacheserver should one be
started.

Usage:
	upspinfs [flags] mountpoint

	where 'mountpoint' is an existing directory upon which to mount the Upspin name space.

The flags are:

	-cachedir directory
		'directory' will contain all file caches (default "$HOME/upspin")
	-cachesize bytes
		max disk bytes for cache (default 5000000000)
	-config file
		user's configuration file (default "$HOME/upspin/config")
	-log level
		level of logging: debug, info, error, disabled (default info)
	-writethrough
		make storage cache writethrough

Examples:

	% mkdir $HOME/ufs
	% upspinfs $HOME/ufs &
	% ls -l $HOME/ufs/tester@tester.com
	% ...
	% killall -9 upspinfs
	% umount $HOME/ufs

Limitations:

Uspinfs tries to present a Posix file system.
However since Upspin semantics are
different than Posix, some things will be different:

- Files always appear owned by the user who started upspinfs

- Permission bits are settable but are not stored in Upspin.
After the final close of a file, when the kernel and FUSE decide to
forget about the file, permissions will revert to 0700.
The access to a file is determined by the intersection of the permission
bits and the relevant Access file.

- While random access will work, the first time a file is opened
for read, it is read in its entirety and cached locally.

- Hard links are really copy on write.
The two names will refer to the original data until either file is changed.
They will then diverge.
*/
package main
