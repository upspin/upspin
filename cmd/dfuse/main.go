// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A FUSE driver for Upspin.
package main

import (
	"flag"
	"fmt"
	"os"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"upspin.io/context"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user/inprocess"
	"upspin.io/user/usercache"

	_ "upspin.io/directory/transports"
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

var (
	testFlag  = flag.String("test", "", "set up test context with specified user")
	debugFlag = flag.Bool("d", false, "turn on debugging")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <mountpoint>\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(2)
}

func debug(msg interface{}) {
	log.Debug.Printf("FUSE %v", msg)
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if *debugFlag {
		fuse.Debug = debug
		log.SetLevel(log.Ldebug)
	}

	if flag.NArg() != 1 {
		usage()
	}
	mountpoint := flag.Arg(0)

	context, err := context.InitContext(nil)
	if err != nil {
		log.Debug.Fatal(err)
	}

	// Turn on caching for users.
	usercache.Install(context)

	// Hack for testing
	if *testFlag != "" {
		testUser, ok := context.User.(*inprocess.Service)
		if !ok {
			log.Debug.Fatal("Not a inprocess.Service")
		}

		if err := testUser.Install(upspin.UserName(*testFlag), context.Directory); err != nil {
			log.Debug.Print(err)
		}
	}

	f := newUpspinFS(context, newDirectoryCache(context))

	c, err := fuse.Mount(
		mountpoint,
		fuse.FSName("upspin"),
		fuse.Subtype("fs"),
		fuse.LocalVolume(),
		fuse.VolumeName(string(f.context.UserName)),
		fuse.DaemonTimeout("240"),
		//fuse.OSXDebugFuseKernel(),
		fuse.NoAppleDouble(),
		fuse.NoAppleXattr(),
	)
	if err != nil {
		log.Debug.Fatal(err)
	}
	defer c.Close()

	err = fs.Serve(c, f)
	if err != nil {
		log.Debug.Fatal(err)
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		log.Debug.Fatal(err)
	}
}
