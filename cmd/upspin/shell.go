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
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	promptFlag := fs.String("prompt", "u> ", "interactive prompt")
	fs.Usage = subUsage(fs, "shell")
	err := fs.Parse(args)
	if err != nil {
		s.exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
	}
	prompt := func() {
		if len(*promptFlag) > 0 {
			fmt.Print(*promptFlag)
		}
	}
	s.interactive = true
	defer func() { s.interactive = false }()
	scanner := bufio.NewScanner(os.Stdin)
	for prompt(); scanner.Scan(); prompt() {
		s.exec(scanner.Text())
	}
	if scanner.Err() != nil {
		s.exit(scanner.Err())
	}
}

func (s *State) exec(line string) {
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
	fn := commands[strings.ToLower(words[0])]
	if fn == nil {
		fmt.Fprintf(os.Stderr, "upspin: no such command %q\n", words[0])
		return
	}
	fn(s, words[1:]...)
}
