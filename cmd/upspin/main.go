// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Upspin is a simple utility for exercising the upspin client against the user's default context.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/endpoint"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports

	_ "upspin.io/directory/transports"
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"
)

var op string // The subcommand we are running.

func main() {
	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) < 1 {
		usage()
	}

	args := flag.Args()[1:]
	op = flag.Arg(0)
	switch strings.ToLower(op) {
	case "get":
		get(args...)
	case "glob":
		glob(args...)
	case "link":
		link(args...)
	case "ls":
		ls(args...)
	case "mkdir":
		mkdir(args...)
	case "put":
		put(args...)
	case "rm":
		rm(args...)
	case "share":
		share(args...)
	case "whichaccess":
		whichAccess(args...)
	default:
		fmt.Fprintf(os.Stderr, "Can't understand command: %v\n", flag.Arg(0))
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of upspin:\n")
	fmt.Fprintf(os.Stderr, "\tupspin [flags] <mkdir|put|get|glob|ls|rm|whichaccess> <path>\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

// exitf prints the error and exits the program.
// We don't use log (although the packages we call do) because the errors
// are for regular people.
func exitf(format string, args ...interface{}) {
	format = fmt.Sprintf("upspin: %s: %s\n", op, format)
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

// exit calls exitf with the error.
func exit(err error) {
	exitf("%s", err)
}

func subUsage(fs *flag.FlagSet, msg string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "Usage: %s\n", msg)
		// How many flags?
		n := 0
		fs.VisitAll(func(*flag.Flag) { n++ })
		if n > 0 {
			fmt.Fprintf(os.Stderr, "Flags:\n")
			fs.PrintDefaults()
		}
		os.Exit(2)
	}
}

func get(args ...string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	fs.Usage = subUsage(fs, "get [-out=outputfile] path")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	c, _ := newClient()
	data, err := c.Get(upspin.PathName(fs.Arg(0)))
	if err != nil {
		exit(err)
	}
	// Write to outfile or to stdout if none set
	var output *os.File
	if *outFile == "" {
		output = os.Stdout
	} else {
		output, err = os.Create(*outFile)
		if err != nil {
			exit(err)
		}
		defer output.Close()
	}
	_, err = output.Write(data)
	if err != nil {
		exitf("Copying to output failed: %v", err)
	}
}

func glob(args ...string) {
	fs := flag.NewFlagSet("glob", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	fs.Usage = subUsage(fs, "glob [-l] pattern...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	c, _ := newClient()
	for i := 0; i < fs.NArg(); i++ {
		de, err := c.Glob(fs.Arg(i))
		if err != nil {
			exit(err)
		}

		if *longFormat {
			printLongDirEntries(c, de)
		} else {
			printShortDirEntries(de)
		}
	}
}

func link(args ...string) {
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	force := fs.Bool("f", false, "create link even if original path does not exist")
	// This is the same order as in the Unix ln command. It sorta feels
	// backwards, but it's also the same as in cp, with the new name second.
	fs.Usage = subUsage(fs, "link original_path new_path")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 2 {
		fs.Usage()
		os.Exit(2)
	}
	originalPath := path.Clean(upspin.PathName(fs.Arg(0)))
	newPath := path.Clean(upspin.PathName(fs.Arg(1)))
	c, _ := newClient()
	// We require the original to exist unless explicitly requested otherwise.
	if !*force {
		dir, err := c.Directory(originalPath)
		if err != nil {
			exit(err)
		}
		if _, err = dir.Lookup(originalPath); err != nil {
			exit(err)
		}
	}
	_, err = c.Link(originalPath, newPath)
	if err != nil {
		exit(err)
	}
}

func ls(args ...string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	fs.Usage = subUsage(fs, "ls [-l] path...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	c, _ := newClient()
	for i := 0; i < fs.NArg(); i++ {
		name := upspin.PathName(fs.Arg(i))
		dir, err := c.Directory(name)
		if err != nil {
			exit(err)
		}
		entry, err := dir.Lookup(name)
		if err != nil {
			exit(err)
		}

		var de []*upspin.DirEntry
		if entry.IsDir() {
			de, err = c.Glob(string(entry.Name) + "/*")
			if err != nil {
				exit(err)
			}
		} else {
			de = []*upspin.DirEntry{entry}
		}

		if *longFormat {
			printLongDirEntries(c, de)
		} else {
			printShortDirEntries(de)
		}
	}
}

func mkdir(args ...string) {
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	fs.Usage = subUsage(fs, "mkdir directory...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	c, _ := newClient()
	for i := 0; i < fs.NArg(); i++ {
		loc, err := c.MakeDirectory(upspin.PathName(fs.Arg(i)))
		if err != nil {
			exit(err)
		}
		fmt.Fprintf(os.Stderr, "%s: %+v\n", fs.Arg(0), loc)
	}
}

func put(args ...string) {
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	fs.Usage = subUsage(fs, "put [-in=inputfile] path")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	c, _ := newClient()
	var input *os.File
	if *inFile == "" {
		input = os.Stdin
	} else {
		input, err = os.Open(*inFile)
		if err != nil {
			exit(err)
		}
		defer input.Close()
	}
	data, err := ioutil.ReadAll(input)
	if err != nil {
		exit(err)
	}
	loc, err := c.Put(upspin.PathName(fs.Arg(0)), data)
	if err != nil {
		exit(err)
	}
	fmt.Fprintf(os.Stderr, "%s: %+v\n", fs.Arg(0), loc)
}

func rm(args ...string) {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	fs.Usage = subUsage(fs, "rm path...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	_, ctx := newClient()
	dir, err := bind.Directory(ctx, ctx.DirectoryEndpoint)
	if err != nil {
		exit(err)
	}
	for i := 0; i < fs.NArg(); i++ {
		err := dir.Delete(upspin.PathName(fs.Arg(i)))
		if err != nil {
			exit(err)
		}
	}
}

func share(args ...string) {
	fs := flag.NewFlagSet("share", flag.ExitOnError)
	fix := fs.Bool("fix", false, "repair incorrect share settings")
	force := fs.Bool("force", false, "replace wrapped keys regardless of current state")
	isDir := fs.Bool("d", false, "do all files in directory; path must be a directory")
	recur := fs.Bool("r", false, "recur into subdirectories; path must be a directory. assumes -d")
	quiet := fs.Bool("q", false, "suppress output. Default is to show state for every file")
	fs.Usage = subUsage(fs, "share path...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 1 {
		usage()
	}
	if *recur {
		*isDir = true
	}
	if *force {
		*fix = true
	}
	s := &sharer{
		fs:    fs,
		fix:   *fix,
		force: *force,
		isDir: *isDir,
		recur: *recur,
		quiet: *quiet,
	}
	s.do()
}

func whichAccess(args ...string) {
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	fs.Usage = subUsage(fs, "whichaccess path...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	_, ctx := newClient()
	dir, err := bind.Directory(ctx, ctx.DirectoryEndpoint)
	if err != nil {
		exit(err)
	}
	for i := 0; i < fs.NArg(); i++ {
		acc, err := dir.WhichAccess(upspin.PathName(fs.Arg(i)))
		if err != nil {
			exit(err)
		}
		fmt.Println(acc)
	}
}

func hasFinalSlash(name upspin.PathName) bool {
	return strings.HasSuffix(string(name), "/")
}

func printShortDirEntries(de []*upspin.DirEntry) {
	for _, e := range de {
		if e.IsDir() && !hasFinalSlash(e.Name) {
			fmt.Printf("%s/\n", e.Name)
		} else {
			fmt.Printf("%s\n", e.Name)
		}
	}
}

func printLongDirEntries(c upspin.Client, de []*upspin.DirEntry) {
	seqWidth := 2
	sizeWidth := 2
	for _, e := range de {
		s := fmt.Sprintf("%d", e.Metadata.Sequence)
		if seqWidth < len(s) {
			seqWidth = len(s)
		}
		s = fmt.Sprintf("%d", e.Metadata.Size)
		if sizeWidth < len(s) {
			sizeWidth = len(s)
		}
	}
	for _, e := range de {
		redirect := ""
		attrChar := ' '
		if e.IsDir() {
			attrChar = 'd'
			if !hasFinalSlash(e.Name) {
				e.Name += "/"
			}
		}
		if e.IsLink() {
			attrChar = '>'
			data, err := c.Get(e.Name)
			if err == nil {
				redirect = " -> " + string(data)
			} else {
				fmt.Fprintf(os.Stderr, "fetching redirect for %q: %s\n", e.Name, err)
				redirect = " (error fetching redirect)"
			}

		}
		endpt := endpoint.String(&e.Location.Endpoint)
		packStr := "?"
		packer := lookupPacker(e)
		if packer != nil {
			packStr = packer.String()
		}
		fmt.Printf("%c %-6s %*d %*d %s [%s]\t%s%s\n",
			attrChar,
			packStr,
			seqWidth, e.Metadata.Sequence,
			sizeWidth, e.Metadata.Size,
			e.Metadata.Time.Go().Local().Format("Mon Jan _2 15:04:05"),
			endpt,
			e.Name,
			redirect)
	}
}

func newClient() (upspin.Client, *upspin.Context) {
	ctx, err := context.InitContext(nil)
	if err != nil {
		exit(err)
	}
	return client.New(ctx), ctx
}
