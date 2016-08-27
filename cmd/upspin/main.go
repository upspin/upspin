// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Upspin is a simple utility for exercising the upspin client against the user's default context.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports

	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"
)

var commands = map[string]func(...string){
	"countersign": countersign,
	"get":         get,
	"glob":        glob,
	"info":        info,
	"link":        link,
	"ls":          ls,
	"mkdir":       mkdir,
	"put":         put,
	"rotate":      rotate,
	"rm":          rm,
	"share":       share,
	"user":        user,
	"whichaccess": whichAccess,
}

var (
	op          string  // The subcommand we are running.
	interactive = false // Whether we are running the shell.
)

type State struct {
	client  upspin.Client
	context upspin.Context
}

var state State

func main() {
	flag.Usage = usage
	flags.Parse() // enable all flags

	if len(flag.Args()) < 1 {
		usage()
	}

	state.client, state.context = newClient()

	args := flag.Args()[1:]
	op = strings.ToLower(flag.Arg(0))
	// Shell cannot be in commands because of the initialization loop,
	// and anyway we should avoid recursion in the interpreter.
	if op == "shell" {
		shell(args...)
		return
	}
	fn := commands[op]
	if fn == nil {
		fmt.Fprintf(os.Stderr, "upspin: no such command %q\n", flag.Arg(0))
		usage()
	}
	fn(args...)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of upspin:\n")
	fmt.Fprintf(os.Stderr, "\tupspin [globalflags] <command> [flags] <path>\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	var cmdStrs []string
	for cmd := range commands {
		cmdStrs = append(cmdStrs, cmd)
	}
	sort.Strings(cmdStrs)
	fmt.Fprintf(os.Stderr, "\tshell (Interactive mode)\n")
	for _, cmd := range cmdStrs {
		fmt.Fprintf(os.Stderr, "\t%s\n", cmd)
	}
	fmt.Fprintf(os.Stderr, "Global flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

// exitf prints the error and exits the program.
// If we are interactive, it pops up to the interpreter.
// We don't use log (although the packages we call do) because the errors
// are for regular people.
func exitf(format string, args ...interface{}) {
	format = fmt.Sprintf("upspin: %s: %s\n", op, format)
	fmt.Fprintf(os.Stderr, format, args...)
	if interactive {
		panic("exit")
	}
	os.Exit(1)
}

// exit calls exitf with the error.
func exit(err error) {
	exitf("%s", err)
}

func subUsage(fs *flag.FlagSet, msg string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "Usage: upspin %s\n", msg)
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

func countersign(args ...string) {
	fs := flag.NewFlagSet("countersign", flag.ExitOnError)
	fs.Usage = subUsage(fs, "countersign")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
	}
	countersigner.init()
	countersigner.countersignCommand()
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

	data, err := state.client.Get(upspin.PathName(fs.Arg(0)))
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
	for i := 0; i < fs.NArg(); i++ {
		de, err := state.client.Glob(fs.Arg(i))
		if err != nil {
			exit(err)
		}

		if *longFormat {
			printLongDirEntries(de)
		} else {
			printShortDirEntries(de)
		}
	}
}

func info(args ...string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	fs.Usage = subUsage(fs, "info path...")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	for i := 0; i < fs.NArg(); i++ {
		name := upspin.PathName(fs.Arg(i))
		// We don't want to follow links, so don't use Client.
		dir, err := bind.DirServer(state.context, state.context.DirEndpoint())
		if err != nil {
			exit(err)
		}
		entry, err := dir.Lookup(name)
		if err != nil {
			exit(err)
		}
		printInfo(entry)
	}
}

func link(args ...string) {
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	// This is the same order as in the Unix ln command. It sorta feels
	// backwards, but it's also the same as in cp, with the new name second.
	fs.Usage = subUsage(fs, "link original_path link_path")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 2 {
		fs.Usage()
		os.Exit(2)
	}
	originalPath := path.Clean(upspin.PathName(fs.Arg(0)))
	linkPath := path.Clean(upspin.PathName(fs.Arg(1)))
	_, err = state.client.PutLink(originalPath, linkPath)
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
	for i := 0; i < fs.NArg(); i++ {
		name := upspin.PathName(fs.Arg(i))
		// Note: this follows links. This is not what Unix ls does.
		// If you want to know about the link itself, info will tell you.
		// TODO: Is this what we want?
		entry, err := state.client.Lookup(name, false)
		if err != nil {
			exit(err)
		}

		var de []*upspin.DirEntry
		if entry.IsDir() {
			de, err = state.client.Glob(string(entry.Name) + "/*")
			if err != nil {
				exit(err)
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
		exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		_, err := state.client.MakeDirectory(upspin.PathName(fs.Arg(i)))
		if err != nil {
			exit(err)
		}
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
	data := readAll(*inFile)
	_, err = state.client.Put(upspin.PathName(fs.Arg(0)), data)
	if err != nil {
		exit(err)
	}
}

func rotate(args ...string) {
	fs := flag.NewFlagSet("rotate", flag.ExitOnError)
	fs.Usage = subUsage(fs, "rotate")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}
	ctx := state.context
	f := ctx.Factotum()      // save new key
	ctx.SetFactotum(f.Pop()) // ctx now defaults to old key
	keyServer, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		exit(err)
	}
	u, err := keyServer.Lookup(ctx.UserName())
	if err != nil {
		exit(err)
	}
	u.PublicKey = f.PublicKey()
	err = keyServer.Put(u)
	if err != nil {
		exit(err)
	}
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
	for i := 0; i < fs.NArg(); i++ {
		err := state.client.Delete(upspin.PathName(fs.Arg(i)))
		if err != nil {
			// TODO: client must implement Delete so links work.
			exit(err)
		}
	}
}

func user(args ...string) {
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	put := fs.Bool("put", false, "write new user record")
	inFile := fs.String("in", "", "input file (default standard input)")
	force := fs.Bool("force", false, "force writing user record even if key is empty")
	// TODO: the username is not accepted with -put. We may need two lines to fix this (like 'man printf').
	fs.Usage = subUsage(fs, "user [-put [-in=inputfile] [-force]] [username...]")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	keyServer, err := bind.KeyServer(state.context, state.context.KeyEndpoint())
	if err != nil {
		exit(err)
	}
	if *put {
		if fs.NArg() != 0 {
			fs.Usage()
			os.Exit(2)
		}
		putUser(keyServer, *inFile, *force)
		return
	}
	if *inFile != "" {
		exitf("-in only available with -put")
	}
	if *force {
		exitf("-force only available with -put")
	}
	var userNames []upspin.UserName
	if fs.NArg() == 0 {
		userNames = append(userNames, state.context.UserName())
	} else {
		for i := 0; i < fs.NArg(); i++ {
			userNames = append(userNames, upspin.UserName(fs.Arg(i)))
		}
	}
	for _, name := range userNames {
		u, err := keyServer.Lookup(name)
		if err != nil {
			exit(err)
		}
		blob, err := json.MarshalIndent(u, "", "\t")
		if err != nil {
			// TODO(adg): better error message?
			exit(err)
		}
		fmt.Printf("%s\n", blob)
	}
}

func putUser(keyServer upspin.KeyServer, inFile string, force bool) {
	data := readAll(inFile)
	user := new(upspin.User)
	err := json.Unmarshal(data, user)
	if err != nil {
		// TODO(adg): better error message?
		exit(err)
	}
	// Validate public key.
	if user.PublicKey == "" && !force {
		exitf("An empty public key will prevent user from accessing services. To override use -force.")
	}
	_, _, err = factotum.ParsePublicKey(user.PublicKey)
	if err != nil && !force {
		exitf("invalid public key, to override use -force: %s", err.Error())
	}
	// Validate username
	_, _, err = path.UserAndDomain(user.Name)
	if err != nil {
		exit(err)
	}
	err = keyServer.Put(user)
	if err != nil {
		exit(err)
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
		fs.Usage()
	}
	if *recur {
		*isDir = true
	}
	if *force {
		*fix = true
	}
	sharer.init()
	sharer.fix = *fix
	sharer.force = *force
	sharer.isDir = *isDir
	sharer.recur = *recur
	sharer.quiet = *quiet
	sharer.shareCommand(fs.Args())
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
	for i := 0; i < fs.NArg(); i++ {
		name := upspin.PathName(fs.Arg(i))
		acc, err := whichAccessFollowLinks(state.client, name)
		if err != nil {
			exit(err)
		}
		if acc == nil {
			fmt.Printf("%s: owner only\n", name)
		} else {
			fmt.Printf("%s: %s\n", name, acc.Name)
		}
	}
}

func whichAccessFollowLinks(client upspin.Client, name upspin.PathName) (*upspin.DirEntry, error) {
	for loop := 0; loop < upspin.MaxLinkHops; loop++ {
		dir, err := client.DirServer(name)
		if err != nil {
			exit(err)
		}
		entry, err := dir.WhichAccess(name)
		if err == upspin.ErrFollowLink {
			name = entry.Link
			continue
		}
		if err != nil {
			return nil, err
		}
		return entry, nil
	}
	exitf("%s: link loop", name)
	return nil, nil
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
		s := fmt.Sprintf("%d", e.Sequence)
		if seqWidth < len(s) {
			seqWidth = len(s)
		}
		s = fmt.Sprintf("%d", sizeOf(e))
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
			redirect = " -> " + string(e.Link)
		}
		endpt := ""
		for i := range e.Blocks {
			if i > 0 {
				endpt += ","
			}
			endpt += e.Blocks[i].Location.Endpoint.String()
		}
		packStr := "?"
		packer := lookupPacker(e)
		if packer != nil {
			packStr = packer.String()
		}
		fmt.Printf("%c %-6s %*d %*d %s [%s]\t%s%s\n",
			attrChar,
			packStr,
			seqWidth, e.Sequence,
			sizeWidth, sizeOf(e),
			e.Time.Go().Local().Format("Mon Jan _2 15:04:05"),
			endpt,
			e.Name,
			redirect)
	}
}

func sizeOf(e *upspin.DirEntry) int64 {
	size, err := e.Size()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%q: %s\n", e.Name, err)
	}
	return size
}

// readAll reads all contents from an input file name or from stdin if
// the input file name is empty
func readAll(fileName string) []byte {
	var input *os.File
	var err error
	if fileName == "" {
		input = os.Stdin
	} else {
		input, err = os.Open(fileName)
		if err != nil {
			exit(err)
		}
		defer input.Close()
	}

	data, err := ioutil.ReadAll(input)
	if err != nil {
		exit(err)
	}
	return data
}

func newClient() (upspin.Client, upspin.Context) {
	f, err := os.Open(flags.Context)
	if err != nil {
		exitf("reading context: %v", err)
	}
	ctx, err := context.InitContext(f)
	f.Close()
	if err != nil {
		exit(err)
	}
	return client.New(ctx), ctx
}
