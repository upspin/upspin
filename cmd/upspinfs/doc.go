// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !windows

/*
Command upspinfs is a FUSE interface for Upspin. It presents Upspin
files as a locally mounted file system.

If $HOME/upspin/config specifies a cache server endpoint and cacheserver
is not running, upspinfs will attempt to start it. Upspinfs will pass
to cacheserver both its -log and -cache flags.

Usage:
	upspinfs [flags] mountpoint

	where 'mountpoint' is an existing directory upon which to mount the Upspin name space.

The flags are:

	-log=level
 		Set the log level to 'level'.
	-cache=directory
		Cache all files being read or written in 'directory'/fscache

Examples:

	% mkdir $HOME/ufs
	% upspinfs $HOME/ufs &
	% ls -l $HOME/ufs/tester@tester.com
	% ...
	% killall -9 upspinfs
	% umount $HOME/ufs
*/
package main
