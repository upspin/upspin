// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

func (s *State) shell(args ...string) {
	const promptPlaceholder = "<username>"
	const help = `
Shell runs an interactive session for Upspin subcommands.
When running the shell, the leading "upspin" is assumed on each command.
`
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	promptFlag := fs.String("prompt", promptPlaceholder, "interactive `prompt`")
	verbose := fs.Bool("v", false, "verbose; print to stderr each command before execution")
	s.ParseFlags(fs, args, help, "shell [-v] [-prompt=<prompt_string>]")
	if fs.NArg() != 0 {
		fs.Usage()
	}
	prompt := func() {
		if len(*promptFlag) > 0 {
			fmt.Fprint(os.Stderr, *promptFlag)
		}
	}
	if *promptFlag == promptPlaceholder {
		*promptFlag = string(s.Config.UserName()) + ">"
	}
	s.Interactive = true
	defer func() { s.Interactive = false }()
	scanner := bufio.NewScanner(os.Stdin)
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
	// TODO: quoting.
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
		fmt.Fprintf(os.Stderr, "upspin: no such command %q\n", words[0])
		return
	}
	if verbose {
		fmt.Fprintln(os.Stderr, " + "+strings.Join(words, " "))
	}
	s.Name = words[0]
	fn(s, words[1:]...)
}
