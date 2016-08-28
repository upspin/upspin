// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A FUSE driver for Upspin.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/key/usercache"
	"upspin.io/log"
	"upspin.io/upspin"

	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/store/transports"
)

var (
	testFlag = flag.String("test", "", "set up test context with specified user")
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
	flags.Parse()

	if log.Level() == "debug" {
		fuse.Debug = debug
	}

	if flag.NArg() != 1 {
		usage()
	}
	mountpoint, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatal("can't determine absolute path to mount point %s: %s", flag.Arg(0), err)
	}

	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		log.Fatal(err)
	}

	// dfuse does not do user lookups, so it does not need a usercache (other layers will use it).

	// Hack for testing
	if *testFlag != "" {
		key, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
		if err != nil {
			log.Debug.Fatal(err)
		}
		if ep := key.Endpoint(); ep.Transport != upspin.InProcess {
			log.Fatal("key server not a inprocess.Service")
		}
		// Validate context.
		if _, err = bind.DirServer(ctx, ctx.DirEndpoint()); err != nil {
			log.Debug.Fatal(err)
		}
		if _, err := bind.StoreServer(ctx, ctx.StoreEndpoint()); err != nil {
			log.Debug.Fatal(err)
		}
		user := &upspin.User{
			Name:   upspin.UserName(*testFlag),
			Dirs:   []upspin.Endpoint{ctx.DirEndpoint()},
			Stores: []upspin.Endpoint{ctx.StoreEndpoint()},
		}
		if err := key.Put(user); err != nil {
			log.Debug.Print(err)
		}
	}

	ctx = usercache.Global(ctx)
	f := newUpspinFS(ctx, mountpoint)

	c, err := fuse.Mount(
		mountpoint,
		fuse.FSName("upspin"),
		fuse.Subtype("fs"),
		fuse.LocalVolume(),
		fuse.VolumeName(string(f.context.UserName())),
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
