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

func shell(args ...string) {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	fs.Usage = subUsage(fs, "shell")
	err := fs.Parse(args)
	if err != nil {
		exit(err)
	}
	if fs.NArg() != 0 {
		fs.Usage()
	}
	interactive = true
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("u> ")
	for scanner.Scan() {
		exec(scanner.Text())
		fmt.Print("u> ")
	}
	interactive = false
	if scanner.Err() != nil {
		exit(scanner.Err())
	}
}

func exec(line string) {
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
	fn(words[1:]...)
}
