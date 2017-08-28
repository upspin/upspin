// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"strings"
)

func (s *State) shell(args ...string) {
	const promptPlaceholder = "<username>"
	const help = `
Shell runs an interactive session for Upspin subcommands.
When running the shell, the leading "upspin" is assumed on each command.

The shell has a simple interface, free of quoting or other features usually
associated with interactive shells. It is intended only for testing and is kept
simple for reasons of comprehensibility, portability, and maintainability.
Those who need quoting or line editing or other such features should use their
regular shell and run upspinfs or invoke the upspin command line-by-line.

The shell does have one convenience feature, though, in the handling of path
names. A path beginning with a plain @ refers to the current user's root
(ann@example.com), while one starting @+suffix is the same with the suffix
included (ann+suffix@example.com). This feature works in all upspin commands
but is particularly handy inside the shell.
`
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	promptFlag := fs.String("prompt", promptPlaceholder, "interactive `prompt`")
	verbose := fs.Bool("v", false, "verbose; print to stderr each command before execution")
	s.ParseFlags(fs, args, help, "shell [-v] [-prompt=<prompt_string>]")
	if fs.NArg() != 0 {
		usageAndExit(fs)
	}
	prompt := func() {
		if len(*promptFlag) > 0 {
			fmt.Fprint(s.Stderr, *promptFlag)
		}
	}
	if *promptFlag == promptPlaceholder {
		*promptFlag = string(s.Config.UserName()) + ">"
	}
	s.Interactive = true
	defer func() { s.Interactive = false }()
	scanner := bufio.NewScanner(s.Stdin)
	for prompt(); scanner.Scan(); prompt() {
		s.exec(scanner.Text(), *verbose)
	}
	if scanner.Err() != nil {
		s.Exit(scanner.Err())
	}
}

func (s *State) exec(line string, verbose bool) {
	defer func() {
		err := recover()
		if err != nil {
			if str, ok := err.(string); ok && str == "exit" {
				// OK; this was a subcommand calling exit
			} else {
				panic(err)
			}
		}
	}()
	line = strings.TrimSpace(line)
	sharp := strings.IndexByte(line, '#')
	if sharp >= 0 {
		line = line[:sharp]
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return
	}
	fn := s.getCommand(strings.ToLower(words[0]))
	if fn == nil {
		fmt.Fprintf(s.Stderr, "upspin: no such command %q\n", words[0])
		return
	}
	if verbose {
		fmt.Fprintln(s.Stderr, " + "+strings.Join(words, " "))
	}
	s.Name = words[0]
	fn(s, words[1:]...)
}
