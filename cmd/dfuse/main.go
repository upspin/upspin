// A FUSE driver for Upspin.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"bazil.org/fuse"
	"bazil.org/fuse/fs"

	"upspin.googlesource.com/upspin.git/context"
	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
	testuser "upspin.googlesource.com/upspin.git/user/testuser"
)

var (
	testFlag = flag.Bool("test", false, "set up test context.")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <mountpoint>\n", os.Args[0])
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
	}
	mountpoint := flag.Arg(0)

	context, err := context.InitContext(nil)
	if err != nil {
		log.Fatal(err)
	}

	// Hack for testing
	if *testFlag {
		testUser, ok := context.User.(*testuser.Service)
		if !ok {
			log.Fatal("Not a testuser Service")
		}

		if err := testUser.Install("testuser@google.com", context.Directory); err != nil {
			log.Print(err)
		}
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
