// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate ./mkdoc.sh

package main

import (
	"bytes"
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

	"upspin.io/cmd/cacheserver/cacheutil"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/subcmd"
	"upspin.io/upspin"
	"upspin.io/version"

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

As a shorthand, a path beginning with a plain @ refers to the current
user's root (ann@example.com), while one starting @+suffix is the
same with the suffix included (ann+suffix@example.com).

For a list of available subcommands and global flags, run

	upspin -help
`

var commands = map[string]func(*State, ...string){
	"countersign":        (*State).countersign,
	"cp":                 (*State).cp,
	"config":             (*State).config,
	"createsuffixeduser": (*State).createsuffixeduser,
	"deletestorage":      (*State).deletestorage,
	"get":                (*State).get,
	"getref":             (*State).getref,
	"info":               (*State).info,
	"keygen":             (*State).keygen,
	"link":               (*State).link,
	"ls":                 (*State).ls,
	"mkdir":              (*State).mkdir,
	"put":                (*State).put,
	"repack":             (*State).repack,
	"rotate":             (*State).rotate,
	"rm":                 (*State).rm,
	"setupdomain":        (*State).setupdomain,
	"setupserver":        (*State).setupserver,
	"setupwriters":       (*State).setupwriters,
	"share":              (*State).share,
	"signup":             (*State).signup,
	"snapshot":           (*State).snapshot,
	"tar":                (*State).tar,
	"user":               (*State).user,
	"watch":              (*State).watch,
	"whichaccess":        (*State).whichAccess,
}

// externalCommands lists the commands that are considered part of
// the upspin command itself but are implemented as separate binaries.
// We show their documentation when we generate doc.go
var externalCommands = []string{
	"setupstorage",
}

type State struct {
	*subcmd.State
	sharer     *Sharer
	configFile []byte // The contents of the config file we loaded.
}

func main() {
	state, args, ok := setup(flag.CommandLine, os.Args[1:])
	if !ok || len(args) == 0 {
		help()
	}
	if args[0] == "help" {
		help(args[1:]...)
	}
	// Shell cannot be in commands because of the initialization loop,
	// and anyway we should avoid recursion in the interpreter.
	if state.Name == "shell" {
		state.shell(args[1:]...)
		state.ExitNow()
		os.Exit(0)
	}
	state.run(args)
	state.ExitNow()
}

// setup initializes the upspin command given the full command-line argument
// list, args. It applies any global flags set on the command line and returns
// the initialized State and the arg list after the global flags, starting with
// the subcommand ("ls", "info", etc.) that will be run.
func setup(fs *flag.FlagSet, args []string) (*State, []string, bool) {
	log.SetFlags(0)
	log.SetPrefix("upspin: ")
	fs.Usage = usage
	flags.ParseArgsInto(fs, args, flags.Client, "version")
	if flags.Version {
		fmt.Fprint(os.Stdout, version.Version())
		os.Exit(2)
	}
	if len(fs.Args()) < 1 {
		return nil, nil, false
	}
	state := newState(strings.ToLower(fs.Arg(0)))
	state.init()
	// Start the cache if needed.
	if !strings.Contains(state.Name, "setup") && !strings.Contains(state.Name, "signup") {
		cacheutil.Start(state.Config)
	}
	return state, fs.Args(), true
}

// run runs a single command specified by the arguments, which should begin with
// the subcommand ("ls", "info", etc.).
func (state *State) run(args []string) {
	cmd := state.getCommand(args[0])
	cmd(state, args[1:]...)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of upspin:\n")
	fmt.Fprintf(os.Stderr, "\tupspin [globalflags] <command> [flags] <path>\n")
	printCommands()
	fmt.Fprintf(os.Stderr, "Global flags:\n")
	flag.PrintDefaults()
}

// usageAndExit prints usage message from provided FlagSet,
// and exits the program with status code 2.
func usageAndExit(fs *flag.FlagSet) {
	fs.Usage()
	os.Exit(2)
}

// help prints the help for the arguments provided, or if there is none,
// for the command itself.
func help(args ...string) {
	// Find the first non-flag argument.
	cmd := ""
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			cmd = arg
			break
		}
	}
	if cmd == "" {
		fmt.Fprintln(os.Stderr, intro)
	} else {
		// Simplest solution is re-execing.
		command := exec.Command("upspin", cmd, "-help")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.Run()
	}
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
	// Now find all the binaries in the $PATH,
	// but only if we're not generating doc.go.
	if os.Getenv("UPSPIN_GENDOC") == "" {
		cmdStrs = append(cmdStrs, findUpspinBinaries()...)
	} else {
		cmdStrs = append(cmdStrs, externalCommands...)
	}
	// Display "shell" first as it's not in "commands".
	fmt.Fprintf(os.Stderr, "\tshell (Interactive mode)\n")
	sort.Strings(cmdStrs)
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

// getCommand looks up the command named by op.
// If it's in the commands tables, we're done.
// If not, it looks for a binary with the equivalent name
// (upspin foo is implemented by upspin-foo).
// If the command still can't be found, it exits after listing the
// commands that do exist.
func (s *State) getCommand(op string) func(*State, ...string) {
	op = strings.ToLower(op)
	fn := commands[op]
	if fn != nil {
		return fn
	}
	path, err := exec.LookPath("upspin-" + op)
	if err == nil {
		return func(s *State, args ...string) {
			s.runCommand(path, append(flags.Args(), args...)...)
		}
	}
	printCommands()
	s.Exitf("no such command %q", op)
	return nil
}

func (s *State) runCommand(path string, args ...string) {
	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		s.Exit(err)
	}
}

// newState returns a State with enough initialized to run exit, etc.
// It does not contain a Config.
func newState(name string) *State {
	s := &State{
		State: subcmd.NewState(name),
	}
	return s
}

// init initializes the State with what is required to run the subcommand,
// usually including setting up a Config.
func (s *State) init() {
	// signup is special since there is no user yet.
	// keygen simply does not require a config or anything else.
	if s.Name != "signup" && s.Name != "keygen" {
		// Read the config file and pass it to config.InitConfig
		// instead of calling config.FromFile, so that we can stash its
		// contents away for later use by the "config" sub-command.
		data, err := ioutil.ReadFile(flags.Config)
		// Duplicate the logic of config.FromFile that looks for the
		// config in $HOME/upspin/config if it can't be found at its
		// specified location.
		if os.IsNotExist(err) {
			home, err2 := config.Homedir()
			if err2 == nil {
				data, err2 = ioutil.ReadFile(filepath.Join(home, "upspin", flags.Config))
				if err2 == nil {
					err = nil
				}
			}
		}
		if err != nil {
			s.Exit(err)
		}

		cfg, err := config.InitConfig(bytes.NewReader(data))
		if err != nil && err != config.ErrNoFactotum {
			s.Exit(err)
		}
		transports.Init(cfg)
		s.State.Init(cfg)
		s.sharer = newSharer(s)
		s.configFile = data
	}
	s.enableMetrics()
}

func (s *State) Printf(format string, args ...interface{}) {
	fmt.Fprintf(s.Stdout, format, args...)
}

// writeOut writes to the named file or to stdout if it is empty
func (s *State) writeOut(file string, data []byte) {
	// Write to outfile or to stdout if none set
	if file == "" {
		_, err := s.Stdout.Write(data)
		if err != nil {
			s.Exitf("copying to output failed: %v", err)
		}
		return
	}
	output := s.CreateLocal(subcmd.Tilde(file))
	_, err := output.Write(data)
	if err != nil {
		s.Exitf("copying to output failed: %v", err)
	}
	if err := output.Close(); err != nil {
		s.Exitf("closing to output failed: %v", err)
	}
}

// globFlag sets a "-glob=true" flag in the FlagSet.
func globFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("glob", true, "apply glob processing to the arguments")
}

// expandUpspin turns the list of string arguments into Upspin path names.
// If glob is true, it "globs" and @-expands the arguments.
// Otherwise, it interprets leading @ symbols but does no other processing.
func (s *State) expandUpspin(args []string, doGlob bool) []upspin.PathName {
	if doGlob {
		return s.GlobAllUpspinPath(args)
	}
	paths := make([]upspin.PathName, len(args))
	for i, arg := range args {
		paths[i] = upspin.PathName(s.AtSign(arg))
	}
	return paths
}
