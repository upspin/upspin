// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate ./mkdoc.sh

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	// We deliberately use native Go logs for this command-line tool
	// as there is no need to report errors to GCP.
	// Our dependencies will still use the Upspin logs
	"log"

	"upspin.io/cmd/cacheserver/cacheutil"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/metric"
	"upspin.io/subcmd"

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

As a shorthand, a lone at sign (@) at the beginning of an Upspin path
stands for the current user's Upspin root.

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
	"setupwriters":  (*State).setupwriters,
	"share":         (*State).share,
	"signup":        (*State).signup,
	"snapshot":      (*State).snapshot,
	"tar":           (*State).tar,
	"user":          (*State).user,
	"watch":         (*State).watch,
	"whichaccess":   (*State).whichAccess,
}

type State struct {
	*subcmd.State
	sharer       *Sharer
	metricsSaver metric.Saver
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("upspin: ")
	flag.Usage = usage
	flags.Parse(flags.Client)

	if len(flag.Args()) < 1 {
		fmt.Fprintln(os.Stderr, intro)
		os.Exit(2)
	}

	op := strings.ToLower(flag.Arg(0))
	state := newState(op)
	args := flag.Args()[1:]

	// Shell cannot be in commands because of the initialization loop,
	// and anyway we should avoid recursion in the interpreter.
	if state.Name == "shell" {
		// Start the cache if needed.
		state.init()
		cacheutil.Start(state.Config)
		state.shell(args...)
		state.ExitNow()
		return
	}
	cmd := state.getCommand(state.Name)
	state.init()
	cmd(state, args...)
	state.ExitNow()
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
		cfg, err := config.FromFile(flags.Config)
		if err != nil && err != config.ErrNoFactotum {
			s.Exit(err)
		}
		transports.Init(cfg)
		s.State.Init(cfg)
		s.sharer = newSharer(s)
	}
	s.enableMetrics()
	return
}
