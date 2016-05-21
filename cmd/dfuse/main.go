// A FUSE driver for Upspin.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/presotto/fuse"
	"github.com/presotto/fuse/fs"

	"upspin.googlesource.com/upspin.git/context"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"
	"upspin.googlesource.com/upspin.git/user/usercache"

	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/directory/remote"
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
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
		testUser, ok := context.User.(*testuser.Service)
		if !ok {
			log.Debug.Fatal("Not a testuser Service")
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
