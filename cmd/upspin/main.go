// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate ./mkdoc.sh

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	// We deliberately use native Go logs for this command-line tool
	// as there is no need to report errors to GCP.
	// Our dependencies will still use the Upspin logs
	"log"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/cmd/cacheserver/cacheutil"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/metric"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/pack/plain"

	// Load required transports
	"upspin.io/transports"
)

const intro = `
The upspin command provides utilities for creating and administering
Upspin files, users, and servers. Although Upspin data is often
accessed through the host file system using upspinfs, the upspin
command is necessary for other tasks, such as: changing a user's
keys (upspin user); updating the wrapped keys after access permissions
are changed (upspin share); or seeing all the information about an
Upspin file beyond what is visible through the host file system
(upspin info). It can also be used separately from upspinfs to
create, read, and update files.

Each subcommand has a -help flag that explains it in more detail.
For instance

	upspin user -help

explains the purpose and usage of the user subcommand.

There is a set of global flags such as -config to identify the
configuration file to use (default $HOME/upspin/config) and -log
to set the logging level for debugging. These flags apply across
the subcommands.

Each subcommand has its own set of flags, which if used must appear
after the subcommand name. For example, to run the ls command with
its -l flag and debugging enabled, run

	upspin -log debug ls -l

For a list of available subcommands and global flags, run

	upspin -help
`

var commands = map[string]func(*State, ...string){
	"countersign":   (*State).countersign,
	"cp":            (*State).cp,
	"deletestorage": (*State).deletestorage,
	"get":           (*State).get,
	"getref":        (*State).getref,
	"info":          (*State).info,
	"keygen":        (*State).keygen,
	"link":          (*State).link,
	"ls":            (*State).ls,
	"mkdir":         (*State).mkdir,
	"put":           (*State).put,
	"repack":        (*State).repack,
	"rotate":        (*State).rotate,
	"rm":            (*State).rm,
	"setupdomain":   (*State).setupdomain,
	"setupserver":   (*State).setupserver,
	"setupstorage":  (*State).setupstorage,
	"setupwriters":  (*State).setupwriters,
	"share":         (*State).share,
	"signup":        (*State).signup,
	"snapshot":      (*State).snapshot,
	"tar":           (*State).tar,
	"user":          (*State).user,
	"whichaccess":   (*State).whichAccess,
}

type State struct {
	op           string // Name of the subcommand we are running.
	client       upspin.Client
	config       upspin.Config
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
		fmt.Fprintln(os.Stderr, intro)
		os.Exit(2)
	}

	state := newState(strings.ToLower(flag.Arg(0)))
	args := flag.Args()[1:]

	// Start the cache if needed.
	cacheutil.Start(state.config)

	// Shell cannot be in commands because of the initialization loop,
	// and anyway we should avoid recursion in the interpreter.
	if state.op == "shell" {
		state.shell(args...)
		return
	}
	state.getCommand(state.op)(state, args...)
	state.cleanup()
	os.Exit(state.exitCode)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of upspin:\n")
	fmt.Fprintf(os.Stderr, "\tupspin [globalflags] <command> [flags] <path>\n")
	printCommands()
	fmt.Fprintf(os.Stderr, "Global flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

// printCommands shows the available commands, including those installed
// as separate binaries called "upspin-foo".
func printCommands() {
	fmt.Fprintf(os.Stderr, "Upspin commands:\n")
	var cmdStrs []string
	for cmd := range commands {
		if cmd == "gendoc" {
			continue // never show this in usage
		}
		cmdStrs = append(cmdStrs, cmd)
	}
	// Now find all the binaries in the $PATH.
	cmdStrs = append(cmdStrs, findUpspinBinaries()...)
	sort.Strings(cmdStrs)
	fmt.Fprintf(os.Stderr, "\tshell (Interactive mode)\n")
	// There may be dups; filter them.
	prev := ""
	for _, cmd := range cmdStrs {
		if cmd == prev {
			continue
		}
		prev = cmd
		fmt.Fprintf(os.Stderr, "\t%s\n", cmd)
	}
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

// getCommand looks up the command named by op.
// If it's in the commands tables, we're done.
// If not, it looks for a binary with the equivalent name
// (upspin foo is implemented by upspin-foo).
// If the command still can't be found, it exits after listing the
// commands that do exist.
func (s *State) getCommand(op string) func(*State, ...string) {
	fn := commands[op]
	if fn != nil {
		return fn
	}
	path, err := exec.LookPath("upspin-" + op)
	if err == nil {
		return func(s *State, args ...string) {
			s.runCommand(path, args...)
		}
	}
	fmt.Fprintf(os.Stderr, "upspin: no such command %q\n", op)
	printCommands()
	os.Exit(2)
	return nil
}

func (s *State) runCommand(path string, args ...string) {
	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		s.exit(err)
	}
	os.Exit(0)
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

// readAll reads all contents from a local input file or from stdin if
// the input file name is empty
func (s *State) readAll(fileName string) []byte {
	var input *os.File
	var err error
	if fileName == "" {
		input = os.Stdin
	} else {
		input = s.openLocal(fileName)
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
	if op == "signup" || op == "keygen" {
		// signup is special since there is no user yet.
		// keygen simply does not require a config or anything else.
		return s
	}
	cfg, err := config.FromFile(flags.Config)
	if err != nil && err != config.ErrNoFactotum {
		s.exit(err)
	}
	transports.Init(cfg)
	s.client = client.New(cfg)
	s.config = cfg
	s.sharer = newSharer(s)
	s.enableMetrics()
	return s
}

// DirServer returns the DirServer for the root of the name, or exits on failure.
func (s *State) DirServer(name upspin.PathName) upspin.DirServer {
	dir, err := s.client.DirServer(name)
	if err != nil {
		s.exit(err)
	}
	return dir
}

func (s *State) KeyServer() upspin.KeyServer {
	key, err := bind.KeyServer(s.config, s.config.KeyEndpoint())
	if err != nil {
		s.exit(err)
	}
	return key
}

// end terminates any necessary state.
func (s *State) cleanup() {
	s.finishMetrics()
}

// hasGlobChar reports whether the string contains a Glob metacharacter.
func hasGlobChar(pattern string) bool {
	return strings.ContainsAny(pattern, `\*?[`)
}

// globAllUpspin processes the arguments, which should be Upspin paths,
// expanding glob patterns.
func (s *State) globAllUpspin(args []string) []*upspin.DirEntry {
	entries := make([]*upspin.DirEntry, 0, len(args))
	for _, arg := range args {
		entries = append(entries, s.globUpspin(arg)...)
	}
	return entries
}

// globAllUpspinPath processes the arguments, which should be Upspin paths,
// expanding glob patterns. It returns just the paths.
func (s *State) globAllUpspinPath(args []string) []upspin.PathName {
	paths := make([]upspin.PathName, 0, len(args))
	for _, arg := range args {
		paths = append(paths, s.globUpspinPath(arg)...)
	}
	return paths
}

// globUpspin glob-expands the argument, which must be a syntactically
// valid Upspin glob pattern (including a plain path name). If the path does
// not exist, the function exits.
func (s *State) globUpspin(pattern string) []*upspin.DirEntry {
	// Must be a valid Upspin path.
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		s.exit(err)
	}
	// If it has no metacharacters, look it up to be sure it exists.
	if !hasGlobChar(pattern) {
		entry, err := s.client.Lookup(upspin.PathName(pattern), true)
		if err != nil {
			s.exit(err)
		}
		return []*upspin.DirEntry{entry}
	}
	entries, err := s.client.Glob(parsed.String())
	if err != nil {
		s.exit(err)
	}
	return entries
}

// globUpspinPath glob-expands the argument, which must be a syntactically
// valid Upspin glob pattern (including a plain path name). It returns just
// the path names.
func (s *State) globUpspinPath(pattern string) []upspin.PathName {
	// Note: We could call globUpspin but that might do an unnecessary Lookup.
	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		s.exit(err)
	}
	// If it has no metacharacters, leave it alone but clean it.
	if !hasGlobChar(pattern) {
		return []upspin.PathName{path.Clean(upspin.PathName(pattern))}
	}
	entries, err := s.client.Glob(parsed.String())
	if err != nil {
		s.exit(err)
	}
	names := make([]upspin.PathName, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

// globOneUpspin glob-expands the argument, which must result in a
// single Upspin path.
func (s *State) globOneUpspinPath(pattern string) upspin.PathName {
	entries := s.globUpspin(pattern)
	if len(entries) != 1 {
		s.exitf("more than one file matches %s", pattern)
	}
	return entries[0].Name
}

// globOneUpspinNoLinks glob-expands the argument, which must result in a
// single Upspin path. The result must not be a link, but it's OK if it does not
// exist at all.
func (s *State) globOneUpspinNoLinks(pattern string) upspin.PathName {
	// Use Dir not Client to catch links.
	entries, err := s.DirServer(upspin.PathName(pattern)).Glob(pattern)
	if err == upspin.ErrFollowLink {
		s.exitf("%s is a link", entries[0].Name)
	}
	if err != nil {
		s.exit(err)
	}
	if len(entries) > 1 {
		s.exitf("more than one file matches %s", pattern)
	}
	if len(entries) == 0 {
		// No matches; file does not exist. That's OK.
		return upspin.PathName(pattern)
	}
	return entries[0].Name
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

// globOneLocal glob-expands the argument, which must result in a
// single local file name.
func (s *State) globOneLocal(pattern string) string {
	strs := s.globLocal(pattern)
	if len(strs) != 1 {
		s.exitf("more than one file matches %s", pattern)
	}
	return strs[0]
}

func (s *State) openLocal(path string) *os.File {
	f, err := os.Open(path)
	if err != nil {
		s.exit(err)
	}
	return f
}

func (s *State) createLocal(path string) *os.File {
	f, err := os.Create(path)
	if err != nil {
		s.exit(err)
	}
	return f
}

func (s *State) mkdirLocal(path string) {
	err := os.Mkdir(path, 0700)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) mkdirAllLocal(path string) {
	err := os.MkdirAll(path, 0700)
	if err != nil {
		s.exit(err)
	}
}

func (s *State) shouldNotExist(path string) {
	_, err := os.Stat(path)
	if err == nil {
		s.exitf("%s already exists", path)
	}
	if !os.IsNotExist(err) {
		s.exit(err)
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

// stringFlag returns the value of the named string flag in the flag set.
func stringFlag(fs *flag.FlagSet, name string) string {
	return fs.Lookup(name).Value.(flag.Getter).Get().(string)
}
