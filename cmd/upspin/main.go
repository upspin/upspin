// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Upspin is a simple utility for exercising the upspin client against the user's default context.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

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

var (
	c, ctx = newClient()
)

func main() {
	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) < 1 {
		usage()
	}

	args := flag.Args()[1:]
	switch strings.ToLower(flag.Arg(0)) {
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
		log.Fatal(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	data, err := c.Get(upspin.PathName(fs.Arg(0)))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Success reading file. Len: %d", len(data))
	// Write to outfile or to stdout if none set
	var output *os.File
	if *outFile == "" {
		output = os.Stdout
	} else {
		output, err = os.Create(*outFile)
		if err != nil {
			log.Fatal(err)
		}
		defer output.Close()
	}
	_, err = output.Write(data)
	if err != nil {
		log.Fatalf("Copying to output failed: %v", err)
	}
}

func glob(args ...string) {
	fs := flag.NewFlagSet("glob", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	fs.Usage = subUsage(fs, "glob [-l] pattern...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	for i := 0; i < fs.NArg(); i++ {
		de, err := c.Glob(fs.Arg(i))
		if err != nil {
			log.Fatal(err)
		}

		if *longFormat {
			printLongDirEntries(de)
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
		log.Fatal(err)
	}
	if fs.NArg() != 2 {
		fs.Usage()
		os.Exit(2)
	}
	originalPath := path.Clean(upspin.PathName(fs.Arg(0)))
	newPath := path.Clean(upspin.PathName(fs.Arg(1)))
	// We require the original to exist unless explicitly requested otherwise.
	if !*force {
		dir, err := c.Directory(originalPath)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := dir.Lookup(originalPath); err != nil {
			log.Fatal(err)
		}
	}
	_, err = c.Link(originalPath, newPath)
	if err != nil {
		log.Fatal(err)
	}
}

func ls(args ...string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	fs.Usage = subUsage(fs, "ls [-l] path...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	for i := 0; i < fs.NArg(); i++ {
		name := upspin.PathName(fs.Arg(i))
		dir, err := c.Directory(name)
		if err != nil {
			log.Fatal(err)
		}
		entry, err := dir.Lookup(name)
		if err != nil {
			log.Fatal(err)
		}

		var de []*upspin.DirEntry
		if entry.IsDir() {
			de, err = c.Glob(string(entry.Name) + "/*")
			if err != nil {
				log.Fatal(err)
			}
		} else {
			de = []*upspin.DirEntry{entry}
		}

		if *longFormat {
			printLongDirEntries(de)
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
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		loc, err := c.MakeDirectory(upspin.PathName(fs.Arg(i)))
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%s: %+v", fs.Arg(i), loc)
	}
}

func put(args ...string) {
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	fs.Usage = subUsage(fs, "put [-in=inputfile] path")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	var input *os.File
	if *inFile == "" {
		input = os.Stdin
	} else {
		input, err = os.Open(*inFile)
		if err != nil {
			log.Fatal(err)
		}
		defer input.Close()
	}
	data, err := ioutil.ReadAll(input)
	if err != nil {
		log.Fatal(err)
	}
	loc, err := c.Put(upspin.PathName(fs.Arg(0)), data)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: %+v", fs.Arg(0), loc)
}

func rm(args ...string) {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	fs.Usage = subUsage(fs, "rm path...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		err := ctx.Directory.Delete(upspin.PathName(fs.Arg(i)))
		if err != nil {
			log.Fatal(err)
		}
	}
}

func whichAccess(args ...string) {
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	fs.Usage = subUsage(fs, "whichaccess path...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		acc, err := ctx.Directory.WhichAccess(upspin.PathName(fs.Arg(i)))
		if err != nil {
			log.Fatal(err)
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

func printLongDirEntries(de []*upspin.DirEntry) {
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
		attrChar := '_'
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
				log.Printf("Error fetching redirect for %q: %s", e.Name, err)
				redirect = " (error fetching redirect)"
			}

		}
		endpt := endpoint.String(&e.Location.Endpoint)
		// TODO: print readers when we have them again.
		fmt.Printf("%c %*d %*d %s [%s]\t%s%s\n",
			attrChar,
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
		log.Fatal(err)
	}
	return client.New(ctx), ctx
}
