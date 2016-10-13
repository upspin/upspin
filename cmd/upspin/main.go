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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/metric"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"

	// Load useful packers

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports

	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"
)

var commands = map[string]func(*State, ...string){
	"countersign": (*State).countersign,
	"get":         (*State).get,
	"info":        (*State).info,
	"keygen":      (*State).keygen,
	"link":        (*State).link,
	"ls":          (*State).ls,
	"mkdir":       (*State).mkdir,
	"put":         (*State).put,
	"rotate":      (*State).rotate,
	"rm":          (*State).rm,
	"share":       (*State).share,
	"signup":      (*State).signup,
	"user":        (*State).user,
	"whichaccess": (*State).whichAccess,
}

type State struct {
	op            string // Name of the subcommand we are running.
	client        upspin.Client
	context       upspin.Context
	sharer        *Sharer
	countersigner *Countersigner
	exitCode      int // Exit with non-zero status for minor problems.
	interactive   bool
	metricsSaver  metric.Saver
}

func main() {
	flag.Usage = usage
	flags.Parse() // enable all flags

	if len(flag.Args()) < 1 {
		usage()
	}

	state := newState(strings.ToLower(flag.Arg(0)))
	args := flag.Args()[1:]

	// Shell cannot be in commands because of the initialization loop,
	// and anyway we should avoid recursion in the interpreter.
	if state.op == "shell" {
		state.shell(args...)
		return
	}
	fn := commands[state.op]
	if fn == nil {
		fmt.Fprintf(os.Stderr, "upspin: no such command %q\n", flag.Arg(0))
		usage()
	}
	fn(state, args...)
	state.cleanup()
	os.Exit(state.exitCode)
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
func (s *State) exitf(format string, args ...interface{}) {
	format = fmt.Sprintf("upspin: %s: %s\n", s.op, format)
	fmt.Fprintf(os.Stderr, format, args...)
	if s.interactive {
		panic("exit")
	}
	s.cleanup()
	os.Exit(1)
}

// exit calls s.exitf with the error.
func (s *State) exit(err error) {
	s.exitf("%s", err)
}

func (s *State) subUsage(fs *flag.FlagSet, msg string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "Usage: upspin %s\n", msg)
		// How many flags?
		n := 0
		fs.VisitAll(func(*flag.Flag) { n++ })
		if n > 0 {
			fmt.Fprintf(os.Stderr, "Flags:\n")
			fs.PrintDefaults()
		}
		if s.interactive {
			panic("exit")
		}
		os.Exit(2)
	}
}

func (s *State) countersign(args ...string) {
	fs := flag.NewFlagSet("countersign", flag.ExitOnError)
	fs.Usage = s.subUsage(fs, "countersign")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
	}
	s.countersigner = newCountersigner(s)
	s.countersignCommand()
}

func (s *State) get(args ...string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	fs.Usage = s.subUsage(fs, "get [-out=outputfile] path")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}

	if fs.NArg() != 1 {
		fs.Usage()
	}
	names := s.globUpspin(fs.Arg(0))
	if len(names) != 1 {
		fs.Usage()
	}

	data, err := s.client.Get(names[0])
	if err != nil {
		s.exit(err)
	}
	// Write to outfile or to stdout if none set
	var output *os.File
	if *outFile == "" {
		output = os.Stdout
	} else {
		output, err = os.Create(*outFile)
		if err != nil {
			s.exit(err)
		}
		defer output.Close()
	}
	_, err = output.Write(data)
	if err != nil {
		s.exitf("Copying to output failed: %v", err)
	}
}

func (s *State) info(args ...string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	fs.Usage = s.subUsage(fs, "info path...")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for _, name := range s.globAllUpspin(fs.Args()) {
		// We don't want to follow links, so don't use Client.
		entry, err := s.DirServer().Lookup(name)
		if err != nil {
			s.exit(err)
		}
		s.printInfo(entry)
	}
}

func (s *State) keygen(args ...string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	curveName := fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	secret := fs.String("secretseed", "", "128 bit secret `seed` in proquint format")
	where := fs.String("where", "", "`directory` to store keys; default $HOME/.ssh")
	fs.Usage = s.subUsage(fs, "keygen [-curve=256] [-secret=seed] [-where=$HOME/.ssh]")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
	}
	switch *curveName {
	case "p256":
	case "p384":
	case "p521":
		// ok
	default:
		log.Error.Printf("no such curve %q", *curveName)
		fs.Usage()
	}

	public, private, proquintStr, err := createKeys(*curveName, *secret)
	if err != nil {
		s.exitf("creating keys: %v", err)
	}

	keyDir := *where
	if keyDir == "" {
		home := os.Getenv("HOME")
		if len(home) == 0 {
			log.Fatal("no home directory")
		}
		keyDir = filepath.Join(home, ".ssh")
	}
	err = saveKeys(keyDir)
	if err != nil {
		s.exitf("saving previous keys failed(%v); keys not generated", err)
	}
	err = writeKeys(keyDir, public, private, proquintStr)
	if err != nil {
		s.exitf("writing keys: %v", err)
	}
}

func (s *State) signup(args ...string) {
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	force := fs.Bool("force", false, "create a new user even if keys and RC file exist")
	fs.Usage = s.subUsage(fs, "signup email_address [-context=<location of new RC file>]")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
	}

	// User must have a home dir in their native OS.
	homedir, err := context.Homedir()
	if err != nil {
		s.exit(err)
	}

	userName := upspin.UserName(fs.Arg(0))

	uname, suffix, domain, err := user.Parse(userName)
	if err != nil {
		s.exit(err)
	}
	if suffix == "" {
		userName = upspin.UserName(uname)
	} else {
		userName = upspin.UserName(uname + "+" + suffix)
	}
	userName = userName + upspin.UserName("@"+domain)

	// Figure out location of the RC file.
	rcFile := filepath.Join(homedir, "/upspin/rc")
	if flags.Context != "" {
		// Context can be relative to the user's home dir or absolute.
		if flags.Context[0] == '/' {
			rcFile = flags.Context
		} else {
			rcFile = filepath.Join(homedir, rcFile)
		}
	}

	// Verify if we have an RC file.
	_, err = context.FromFile(rcFile)
	if err == nil && !*force {
		s.exitf(rcFile + " already exists")
	}

	// Create an RC file for this new user.
	const rcTemplate = "username=%s\nkeyserver=%s\n"
	const defaultKeyServer = "remote,key.upspin.io:443"

	rcContents := fmt.Sprintf(rcTemplate, userName, defaultKeyServer)
	fileMode := os.FileMode(0640) // rw-r---
	err = ioutil.WriteFile(rcFile, []byte(rcContents), fileMode)
	if err != nil {
		s.exit(err)
	}

	// Generate a new key.
	s.keygen()
	// TODO: write better instructions.
	fmt.Println("Write down the code above. It is used to recover your keys.")

	// Now load the context. This time it should succeed.
	ctx, err := context.InitContext(nil)
	if err != nil {
		s.exit(err)
	}

	pubKey := strings.TrimSpace(string(ctx.Factotum().PublicKey()))

	// Sign the username and key.
	sig, err := ctx.Factotum().UserSign([]byte(string(ctx.UserName()) + pubKey))
	if err != nil {
		s.exit(err)
	}

	const mailTemplate = `I am %s
My public key is
%s
Signature:
%s:%s
`
	msg := fmt.Sprintf(mailTemplate, ctx.UserName(), pubKey,
		sig.R.String(), sig.S.String())

	fmt.Printf("To complete your registration, send email to signup@key.upspin.io with the following contents:\n%s\n", msg)
}

func (s *State) link(args ...string) {
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	// This is the same order as in the Unix ln command. It sorta feels
	// backwards, but it's also the same as in cp, with the new name second.
	fs.Usage = s.subUsage(fs, "link original_path link_path")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 2 {
		fs.Usage()
	}

	originalPath := s.globUpspin(fs.Arg(0))
	linkPath := s.globUpspin(fs.Arg(1))
	if len(originalPath) != 1 || len(linkPath) != 1 {
		fs.Usage()
	}

	_, err = s.client.PutLink(originalPath[0], linkPath[0])
	if err != nil {
		s.exit(err)
	}
}

func (s *State) ls(args ...string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	followLinks := fs.Bool("L", false, "follow links")
	recur := fs.Bool("R", false, "recur into subdirectories")
	fs.Usage = s.subUsage(fs, "ls [-l] [path...]")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	done := map[upspin.PathName]bool{}
	if fs.NArg() == 0 {
		userRoot := upspin.PathName(s.context.UserName())
		s.list(userRoot, done, *longFormat, *followLinks, *recur)
		return
	}
	// The done map marks a directory we have listed, so we don't recur endlessly
	// when given a chain of links with -L.
	for _, name := range s.globAllUpspin(fs.Args()) {
		s.list(name, done, *longFormat, *followLinks, *recur)
	}
}

func (s *State) list(name upspin.PathName, done map[upspin.PathName]bool, longFormat, followLinks, recur bool) {
	done[name] = true
	entry, err := s.client.Lookup(name, followLinks)
	if err != nil {
		s.exit(err)
	}

	var dirContents []*upspin.DirEntry
	if entry.IsDir() {
		dirContents, err = s.client.Glob(string(entry.Name) + "/*")
		if err != nil {
			s.exit(err)
		}
	} else {
		dirContents = []*upspin.DirEntry{entry}
	}

	if longFormat {
		printLongDirEntries(dirContents)
	} else {
		printShortDirEntries(dirContents)
	}

	if !recur {
		return
	}
	for _, entry := range dirContents {
		if entry.IsDir() && !done[entry.Name] {
			fmt.Printf("\n%s:\n", entry.Name)
			s.list(entry.Name, done, longFormat, followLinks, recur)
		}
	}
}

func (s *State) mkdir(args ...string) {
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	fs.Usage = s.subUsage(fs, "mkdir directory...")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for _, name := range s.globAllUpspin(fs.Args()) {
		_, err := s.client.MakeDirectory(name)
		if err != nil {
			s.exit(err)
		}
	}
}

func (s *State) put(args ...string) {
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	fs.Usage = s.subUsage(fs, "put [-in=inputfile] path")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
	}

	name := s.globUpspin(fs.Arg(0))
	if len(name) != 1 {
		fs.Usage()
	}

	data := s.readAll(*inFile)
	_, err = s.client.Put(name[0], data)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) rotate(args ...string) {
	fs := flag.NewFlagSet("rotate", flag.ExitOnError)
	fs.Usage = s.subUsage(fs, "rotate")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
	}

	f := s.context.Factotum() // save latest factotum
	lastCtx := s.context
	s.context = context.SetFactotum(s.context, f.Pop()) // context now defaults to old key
	defer func() { s.context = lastCtx }()

	keyServer := s.KeyServer()
	u, err := keyServer.Lookup(s.context.UserName())
	if err != nil {
		s.exit(err)
	}
	u.PublicKey = f.PublicKey()
	err = keyServer.Put(u)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) rm(args ...string) {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	fs.Usage = s.subUsage(fs, "rm path...")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for _, name := range s.globAllUpspin(fs.Args()) {
		err := s.client.Delete(name)
		if err != nil {
			s.exit(err)
		}
	}
}

func (s *State) user(args ...string) {
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	put := fs.Bool("put", false, "write new user record")
	inFile := fs.String("in", "", "input file (default standard input)")
	force := fs.Bool("force", false, "force writing user record even if key is empty")
	// TODO: the username is not accepted with -put. We may need two lines to fix this (like 'man printf').
	fs.Usage = s.subUsage(fs, "user [-put [-in=inputfile] [-force]] [username...]")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	keyServer := s.KeyServer()
	if *put {
		if fs.NArg() != 0 {
			fs.Usage()
		}
		s.putUser(keyServer, *inFile, *force)
		return
	}
	if *inFile != "" {
		s.exitf("-in only available with -put")
	}
	if *force {
		s.exitf("-force only available with -put")
	}
	var userNames []upspin.UserName
	if fs.NArg() == 0 {
		userNames = append(userNames, s.context.UserName())
	} else {
		for i := 0; i < fs.NArg(); i++ {
			userNames = append(userNames, upspin.UserName(fs.Arg(i)))
		}
	}
	for _, name := range userNames {
		u, err := keyServer.Lookup(name)
		if err != nil {
			s.exit(err)
		}
		blob, err := json.MarshalIndent(u, "", "\t")
		if err != nil {
			// TODO(adg): better error message?
			s.exit(err)
		}
		fmt.Printf("%s\n", blob)
	}
}

func (s *State) putUser(keyServer upspin.KeyServer, inFile string, force bool) {
	data := s.readAll(inFile)
	userStrct := new(upspin.User)
	err := json.Unmarshal(data, userStrct)
	if err != nil {
		// TODO(adg): better error message?
		s.exit(err)
	}
	// Validate public key.
	if userStrct.PublicKey == "" && !force {
		s.exitf("An empty public key will prevent user from accessing services. To override use -force.")
	}
	_, _, err = factotum.ParsePublicKey(userStrct.PublicKey)
	if err != nil && !force {
		s.exitf("invalid public key, to override use -force: %s", err.Error())
	}
	// Validate username
	_, _, _, err = user.Parse(userStrct.Name)
	if err != nil {
		s.exit(err)
	}
	err = keyServer.Put(userStrct)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) share(args ...string) {
	fs := flag.NewFlagSet("share", flag.ExitOnError)
	fix := fs.Bool("fix", false, "repair incorrect share settings")
	force := fs.Bool("force", false, "replace wrapped keys regardless of current state")
	isDir := fs.Bool("d", false, "do all files in directory; path must be a directory")
	recur := fs.Bool("r", false, "recur into subdirectories; path must be a directory. assumes -d")
	quiet := fs.Bool("q", false, "suppress output. Default is to show state for every file")
	fs.Usage = s.subUsage(fs, "share path...")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}

	if *recur {
		*isDir = true
	}
	if *force {
		*fix = true
	}
	s.sharer.fix = *fix
	s.sharer.force = *force
	s.sharer.isDir = *isDir
	s.sharer.recur = *recur
	s.sharer.quiet = *quiet
	s.shareCommand(s.globAllUpspin(fs.Args()))
}

func (s *State) whichAccess(args ...string) {
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	fs.Usage = s.subUsage(fs, "whichaccess path...")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for _, name := range s.globAllUpspin(fs.Args()) {
		acc, err := s.whichAccessFollowLinks(name)
		if err != nil {
			s.exit(err)
		}
		if acc == nil {
			fmt.Printf("%s: owner only\n", name)
		} else {
			fmt.Printf("%s: %s\n", name, acc.Name)
		}
	}
}

func (s *State) whichAccessFollowLinks(name upspin.PathName) (*upspin.DirEntry, error) {
	for loop := 0; loop < upspin.MaxLinkHops; loop++ {
		dir, err := s.client.DirServer(name)
		if err != nil {
			s.exit(err)
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
	s.exitf("%s: link loop", name)
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
func (s *State) readAll(fileName string) []byte {
	var input *os.File
	var err error
	if fileName == "" {
		input = os.Stdin
	} else {
		input, err = os.Open(fileName)
		if err != nil {
			s.exit(err)
		}
		defer input.Close()
	}

	data, err := ioutil.ReadAll(input)
	if err != nil {
		s.exit(err)
	}
	return data
}

func newState(op string) *State {
	s := &State{
		op: op,
	}
	if op == "signup" {
		// signup is special since there is no user yet.
		return s
	}
	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		s.exit(err)
	}
	s.client = client.New(ctx)
	s.context = ctx
	s.sharer = newSharer(s)
	s.maybeEnableMetrics()
	return s
}

func (s *State) DirServer() upspin.DirServer {
	dir, err := bind.DirServer(s.context, s.context.DirEndpoint())
	if err != nil {
		s.exit(err)
	}
	return dir
}

func (s *State) KeyServer() upspin.KeyServer {
	key, err := bind.KeyServer(s.context, s.context.KeyEndpoint())
	if err != nil {
		s.exit(err)
	}
	return key
}

// end terminates any necessary state.
func (s *State) cleanup() {
	s.finishMetricsIfEnabled()
}

func (s *State) maybeEnableMetrics() {
	gcloudProject := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if strings.Contains(gcloudProject, "upspin-test") {
		gcloudProject = "upspin-test"
	} else if strings.Contains(gcloudProject, "upspin-prod") {
		gcloudProject = "upspin-prod"
	} else {
		return
	}
	var err error
	if s.metricsSaver, err = metric.NewGCPSaver(gcloudProject, "app", "cmd/upspin"); err == nil {
		metric.RegisterSaver(s.metricsSaver)
	} else {
		log.Error.Printf("saving metrics: %q", err)
	}
}

func (s *State) finishMetricsIfEnabled() {
	if s.metricsSaver == nil {
		return
	}
	// Allow time for metrics to propagate.
	for i := 0; metric.NumProcessed() > s.metricsSaver.NumProcessed() && i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

// hasGlobChar reports whether the string contains a Glob metacharacter.
func hasGlobChar(pattern string) bool {
	return strings.ContainsAny(pattern, `\*?[`)
}

// globAllUpspin processes the arguments, which should be Upspin paths,
// expanding glob patterns.
func (s *State) globAllUpspin(args []string) []upspin.PathName {
	paths := make([]upspin.PathName, 0, len(args))
	for _, arg := range args {
		paths = append(paths, s.globUpspin(arg)...)
	}
	return paths
}

// globUpspin glob-expands the argument, which must be a syntactically
// valid Upspin glob pattern (including a plain path name).
func (s *State) globUpspin(pattern string) []upspin.PathName {
	// Must be a valid Upspin path.
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		s.exit(err)
	}
	var out []upspin.PathName
	// If it has no metacharacters, leave it alone.
	if !hasGlobChar(pattern) {
		return append(out, upspin.PathName(pattern))
	}
	entries, err := s.client.Glob(parsed.String())
	if err != nil {
		s.exit(err)
	}
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

// globAllLocal process the arguments, which should be local file paths,
// expanding glob patterns.
// TODO: Unused for now.
func (s *State) globAllLocal(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, s.globLocal(arg)...)
	}
	return out
}

// globLocal glob-expands the argument, which should be a syntactically
// valid glob pattern (including a plain file name).
// TODO: Unused for now.
func (s *State) globLocal(pattern string) []string {
	// If it has no metacharacters, leave it alone.
	if !hasGlobChar(pattern) {
		return []string{pattern}
	}
	strs, err := filepath.Glob(pattern)
	if err != nil {
		// Bad pattern, so treat as a literal.
		return []string{pattern}
	}
	return strs
}
