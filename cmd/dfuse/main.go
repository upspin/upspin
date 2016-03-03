// The upspin fuse interface.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"upspin.googlesource.com/upspin.git/context"
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s MOUNTPOINT\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	mountpoint := flag.Arg(0)

	context, err := context.InitContext(nil)
	if err != nil {
		log.Fatal(err)
	}
	f := newUpspinFS(context, newUserCache(context))

	c, err := fuse.Mount(
		mountpoint,
		fuse.FSName("upspin"),
		fuse.Subtype("fs"),
		fuse.LocalVolume(),
		fuse.VolumeName(string(f.context.UserName)),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	err = fs.Serve(c, f)
	if err != nil {
		log.Fatal(err)
	}

	// check if the mount process has an error to report
	<-c.Ready
	if err := c.MountError; err != nil {
		log.Fatal(err)
	}
}
