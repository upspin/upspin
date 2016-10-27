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

	// We deliberately use native Go logs for this command-line tool
	// as there is no need to report errors to GCP.
	// Our dependencies will still use the Upspin logs
	"log"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/metric"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/user"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports
	"upspin.io/transports"
)

var commands = map[string]func(*State, ...string){
	"countersign": (*State).countersign,
	"cp":          (*State).cp,
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
	"tar":         (*State).tar,
	"untar":       (*State).untar,
	"user":        (*State).user,
	"whichaccess": (*State).whichAccess,
}

type State struct {
	op           string // Name of the subcommand we are running.
	client       upspin.Client
	context      upspin.Context
	sharer       *Sharer
	exitCode     int // Exit with non-zero status for minor problems.
	interactive  bool
	metricsSaver metric.Saver
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("upspin: ")
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

// failf logs the error and sets the exit code. It does not exit the program.
func (s *State) failf(format string, args ...interface{}) {
	format = fmt.Sprintf("upspin: %s: %s\n", s.op, format)
	fmt.Fprintf(os.Stderr, format, args...)
	s.exitCode = 1
}

// fail calls s.failf with the error.
func (s *State) fail(err error) {
	s.failf("%v", err)
}

func (s *State) parseFlags(fs *flag.FlagSet, args []string, help, usage string) {
	helpFlag := fs.Bool("help", false, "print more information about the command")
	usageFn := func() {
		fmt.Fprintf(os.Stderr, "Usage: upspin %s\n", usage)
		if *helpFlag {
			fmt.Fprintln(os.Stderr, help)
		}
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
	fs.Usage = usageFn
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if *helpFlag {
		fs.Usage()
	}
}

func (s *State) countersign(args ...string) {
	const help = `
Countersign updates the signatures and encrypted data for all items
owned by the user. It is intended to be run after a user has changed
keys.

See the description for rotate for information about updating keys.
`
	fs := flag.NewFlagSet("countersign", flag.ExitOnError)
	s.parseFlags(fs, args, help, "countersign")
	if fs.NArg() != 0 {
		fs.Usage()
	}
	s.countersignCommand(fs)
}

func (s *State) get(args ...string) {
	const help = `
Get writes to standard output the contents identified by the Upspin path.

TODO: Delete in favor of cp?
`
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	s.parseFlags(fs, args, help, "get [-out=outputfile] path")

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

func (s *State) cp(args ...string) {
	const help = `
Cp copies files into, out of, and within Upspin. If the final
argument is a directory, the files are placed inside it.  The other
arguments must not be directories unless the -R flag is set.

If the final argument is not a directory, cp requires exactly two
path names and copies the contents of the first to the second.
The -R flag requires that the final argument be a directory.

When copying from one Upspin path to another Upspin path, cp can be
very efficient, copying only the references to the data rather than
the data itself.

The command starts several copies at once to overlap I/O for
efficiency. The -n flag controls the parallelism.
`
	fs := flag.NewFlagSet("cp", flag.ExitOnError)
	n := fs.Int("n", 4, "number of parallel copies to perform; must be > 0")
	fs.Bool("v", false, "log each file as it is copied")
	fs.Bool("R", false, "recursively copy directories")
	s.parseFlags(fs, args, help, "cp [opts] file... file or cp [opts] file... directory")
	if fs.NArg() < 2 || *n <= 0 {
		fs.Usage()
	}

	nSrc := fs.NArg() - 1
	src, dest := fs.Args()[:nSrc], fs.Args()[nSrc]
	s.copyCommand(fs, src, dest)
}

func (s *State) info(args ...string) {
	const help = `
Info prints to standard output a thorough description of all the
information about named paths, including information provided by
ls but also storage references, sizes, and other metadata.
`
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	s.parseFlags(fs, args, help, "info path...")

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
	const help = `
Keygen creates a new Upspin key pair and stores the pair in local
files secret.upspinkey and public.upspinkey in $HOME/.ssh. Existing
key pairs are appended to $HOME/.ssh/secret2.upspinkey. Keygen does
not update the information in the key server; use the user -put
command for that.

New users should instead use the signup command to create their
first key. Keygen can be used to create new keys.

See the description for rotate for information about updating keys.
`
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	curveName := fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	secret := fs.String("secretseed", "", "128 bit secret `seed` in proquint format")
	where := fs.String("where", "", "`directory` to store keys; default $HOME/.ssh")
	s.parseFlags(fs, args, help, "keygen [-curve=256] [-secret=seed] [-where=$HOME/.ssh]")
	if fs.NArg() != 0 {
		fs.Usage()
	}
	switch *curveName {
	case "p256":
	case "p384":
	case "p521":
		// ok
	default:
		log.Printf("no such curve %q", *curveName)
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

func (s *State) link(args ...string) {
	const help = `
Link creates an Upspin link. The link is created at the first path
argument and points to the second path argument.
`
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	// This is the same order as in the Unix ln command. It sorta feels
	// backwards, but it's also the same as in cp, with the new name second.
	s.parseFlags(fs, args, help, "link original_path link_path")
	if fs.NArg() != 2 {
		fs.Usage()
	}

	originalPath := s.globUpspin(fs.Arg(0))
	linkPath := s.globUpspin(fs.Arg(1))
	if len(originalPath) != 1 || len(linkPath) != 1 {
		fs.Usage()
	}

	_, err := s.client.PutLink(originalPath[0], linkPath[0])
	if err != nil {
		s.exit(err)
	}
}

func (s *State) ls(args ...string) {
	const help = `
Ls lists the names and, if requested, other properties of Upspin
files and directories. If given no path arguments, it lists the
user's root. By default ls does not follow links; use the -L flag
to learn about the targets of links.
`
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	followLinks := fs.Bool("L", false, "follow links")
	recur := fs.Bool("R", false, "recur into subdirectories")
	s.parseFlags(fs, args, help, "ls [-l] [path...]")

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
		dirContents, err = s.client.Glob(upspin.AllFilesGlob(entry.Name))
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
	const help = `
Mkdir creates Upspin directories.
`
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	s.parseFlags(fs, args, help, "mkdir directory...")
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
	const help = `
Put writes its input to the store server and installs a directory
entry with the given path name to refer to the data.

TODO: Delete in favor of cp?
`
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	s.parseFlags(fs, args, help, "put [-in=inputfile] path")
	if fs.NArg() != 1 {
		fs.Usage()
	}

	name := s.globUpspin(fs.Arg(0))
	if len(name) != 1 {
		fs.Usage()
	}

	data := s.readAll(*inFile)
	_, err := s.client.Put(name[0], data)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) rotate(args ...string) {
	const help = `
Rotate pushes an updated key to the key server.

To update an Upspin key, the sequence is:

  upspin keygen            # Create new key.
  upspin countersign       # Update file signatures to use new key.
  upspin rotate            # Save new key to key server.
  upspin share -r -fix me@example.com/  # Update keys in file metadata. 

Keygen creates a new key and saves the old one. Countersign walks
the file tree and adds signatures with the new key alongside those
for the old. Rotate pushes the new key to the KeyServer. Share walks
the file tree, re-wrapping the encryption keys that were encrypted
with the old key to use the new key.

Some of these steps could be folded together but the full sequence
makes it easier to recover if a step fails.

TODO: Rotate and countersign are terms of art, not clear to users.
`
	fs := flag.NewFlagSet("rotate", flag.ExitOnError)
	s.parseFlags(fs, args, help, "rotate")
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
	const help = `
Rm removes Upspin files and directories.
`
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	s.parseFlags(fs, args, help, "rm path...")
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

func (s *State) signup(args ...string) {
	const help = `
Signup registers new users with Upspin. It creates a private/public
key pair, stores the private key locally, and prepares to store the
private key with the public upspin key server. It writes an intial
"rc" file into $HOME/upspin/rc, holding the username and the location
of the key server.

As the final step, it writes the contents of a mail message to
standard output. This message contains the public key to be registered
with the key server. After running signup, the new user must mail
this message to signup@key.upspin.io to complete the signup process.

Once this is done, the user should update the rc file to hold the
network addresses of the directory and store servers to use; the
local adminstrator can provide this information.

TODO: The last step should be done automatically. Perhaps signup
should take those two addresses as arguments.
`
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	force := fs.Bool("force", false, "create a new user even if keys and rc file exist")
	rcFile := fs.String("rc", "upspin/rc", "location of the rc file")
	s.parseFlags(fs, args, help, "signup email_address")
	if fs.NArg() != 1 {
		fs.Usage()
	}

	// User must have a home dir in their native OS.
	homedir, err := context.Homedir()
	if err != nil {
		s.exit(err)
	}

	uname, _, domain, err := user.Parse(upspin.UserName(fs.Arg(0)))
	if err != nil {
		s.exit(err)
	}
	userName := upspin.UserName(uname + "@" + domain)

	// Figure out location of the rc file.
	if !filepath.IsAbs(*rcFile) {
		*rcFile = filepath.Join(homedir, *rcFile)
	}
	env := os.Environ()
	wipeUpspinEnvironment()
	defer restoreEnvironment(env)

	// Verify if we have an rc file.
	_, err = context.FromFile(*rcFile)
	if err == nil && !*force {
		s.exitf("%s already exists", *rcFile)
	}

	// Create an rc file for this new user.
	const (
		rcTemplate       = "username=%s\nkeyserver=%s\n"
		defaultKeyServer = "remote,key.upspin.io:443"
	)

	rcContents := fmt.Sprintf(rcTemplate, userName, defaultKeyServer)
	err = ioutil.WriteFile(*rcFile, []byte(rcContents), 0640)
	if err != nil {
		s.exit(err)
	}

	// Generate a new key.
	s.keygen()
	// TODO: write better instructions.
	fmt.Println("Write down the command above. You will need it if you lose your keys.")
	// Now load the context. This time it should succeed.
	ctx, err := context.FromFile(*rcFile)
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

func (s *State) user(args ...string) {
	const help = `
User prints in JSON format the user record stored in the key server
for the specified user, by default the current user.

With the -put flag, user writes or replaces the information stored
for the current user. It can be used to update keys for the user;
for new users see the signup command. The information is read
from standard input or from the file provided with the -in flag.
It must be the complete record for the user, and must be in the
same JSON format printed by the command without the -put flag.
`
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	put := fs.Bool("put", false, "write new user record")
	inFile := fs.String("in", "", "input file (default standard input)")
	force := fs.Bool("force", false, "force writing user record even if key is empty")
	// TODO: the username is not accepted with -put. We may need two lines to fix this (like 'man printf').
	s.parseFlags(fs, args, help, "user [-put [-in=inputfile] [-force]] [username...]")
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
			userName, err := user.Clean(upspin.UserName(fs.Arg(i)))
			if err != nil {
				s.exit(err)
			}
			userNames = append(userNames, userName)
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
	userStruct := new(upspin.User)
	err := json.Unmarshal(data, userStruct)
	if err != nil {
		// TODO(adg): better error message?
		s.exit(err)
	}
	// Validate public key.
	if userStruct.PublicKey == "" && !force {
		s.exitf("An empty public key will prevent user from accessing services. To override use -force.")
	}
	_, _, err = factotum.ParsePublicKey(userStruct.PublicKey)
	if err != nil && !force {
		s.exitf("invalid public key, to override use -force: %s", err.Error())
	}
	// Clean the username.
	userStruct.Name, err = user.Clean(userStruct.Name)
	if err != nil {
		s.exit(err)
	}
	err = keyServer.Put(userStruct)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) share(args ...string) {
	const help = `
Share reports the user names that have access to each of the argument
paths, and what access rights each has. If the access rights do not
agree with the keys stored in the directory metadata for a path,
that is also reported. Given the -fix flag, share updates the keys
to resolve any such inconsistency. Given both -fix and -force, it
updates the keys regardless. The -d and -r flags apply to directories;
-r states whether the share command should descend into subdirectories.

See the description for rotate for information about updating keys.
`
	fs := flag.NewFlagSet("share", flag.ExitOnError)
	fix := fs.Bool("fix", false, "repair incorrect share settings")
	force := fs.Bool("force", false, "replace wrapped keys regardless of current state")
	isDir := fs.Bool("d", false, "do all files in directory; path must be a directory")
	recur := fs.Bool("r", false, "recur into subdirectories; path must be a directory. assumes -d")
	fs.Bool("q", false, "suppress output. Default is to show state for every file")
	s.parseFlags(fs, args, help, "share path...")
	if fs.NArg() == 0 {
		fs.Usage()
	}

	if *recur {
		*isDir = true
	}
	if *force {
		*fix = true
	}
	s.shareCommand(fs)
}

func (s *State) whichAccess(args ...string) {
	const help = `
Whichaccess reports the Upspin path of the Access file
that controls permissions for each of the argument paths.
`
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	s.parseFlags(fs, args, help, "whichaccess path...")
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
		s := fmt.Sprintf("%d", upspin.SeqVersion(e.Sequence))
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
		prevLoc := ""
		for i := range e.Blocks {
			loc := e.Blocks[i].Location.Endpoint.String()
			if loc == prevLoc {
				continue
			}
			prevLoc = loc
			if i > 0 {
				endpt += ","
			}
			endpt += loc
		}
		packStr := "?"
		packer := lookupPacker(e)
		if packer != nil {
			packStr = packer.String()
		}
		fmt.Printf("%c %-6s %*d %*d %s [%s]\t%s%s\n",
			attrChar,
			packStr,
			seqWidth, upspin.SeqVersion(e.Sequence),
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
	transports.Init(ctx)
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
		log.Printf("saving metrics: %q", err)
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
	// If it has no metacharacters, leave it alone but clean it.
	if !hasGlobChar(pattern) {
		return []upspin.PathName{path.Clean(upspin.PathName(pattern))}
	}
	var out []upspin.PathName
	entries, err := s.client.Glob(parsed.String())
	if err != nil {
		s.exit(err)
	}
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

// globLocal glob-expands the argument, which should be a syntactically
// valid glob pattern (including a plain file name).
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

func wipeUpspinEnvironment() {
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "upspin") {
			os.Setenv(env, "")
		}
	}
}

func restoreEnvironment(env []string) {
	for _, e := range env {
		kv := strings.Split(e, "=")
		if len(kv) != 2 {
			continue
		}
		os.Setenv(kv[0], kv[1])
	}
}

// intFlag returns the value of the named integer flag in the flag set.
func intFlag(fs *flag.FlagSet, name string) int {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int)
}

// boolFlag returns the value of the named boolean flag in the flag set.
func boolFlag(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}
