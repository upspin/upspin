// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Cacheserver implements a directory and storage cache for Upspin. It is a
long-lived process that interposes itself between the client and the remote
services, presenting itself as a local HTTP server that behaves just like the
remote ones.

In its default mode, cacheserver runs in writeback mode, which means the
writes are asynchronous and appear to complete quickly, but may take longer to
propagate to the servers. A flag sets writethrough mode instead, which operates
synchronously and more slowly, but also more safely. Cacheserver uses local disk
to store data it has read or written. The size of the local disk area is
configurable with a flag.

The 'cache:' key should be set in the config file to enable the cacheserver.
It takes a single value that can be:

	- 'yes' (or 'y') to use a default endpoint for the cacheserver
	- 'no' (or 'n') to specify no cacheserver (the default)
	- a local TCP port (e.g. localhost:9999) to specify a particular port

The cacheserver will be started automatically by the upspin command or upspinfs if it is
not already running, and continues to run once the program that started it
has exited.

Usage:
	cacheserver [flags]

The flags are:

	-log=level
 		Set the log level to 'level'.
	-cachedir=directory
		Cache all state in 'directory'/{storecache,dircache}.
	-writethrough
		Make storage cache writethrough.
	-cachesize=bytes
		Set the maximum bytes usable for the on disk cache to 'bytes'.

Example $HOME/upspin/config entry:

	cache: yes
	cmdflags:
	 cacheserver:
	  writethrough: true
*/
package main // import "upspin.io/cmd/cacheserver"
