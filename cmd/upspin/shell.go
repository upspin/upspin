// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"runtime"
	"strings"
	"unicode"
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
		usageAndExit(fs)
	}
	prompt := func() {
		if len(*promptFlag) > 0 {
			fmt.Fprint(s.Stderr, *promptFlag)
		}
	}
	if *promptFlag == promptPlaceholder {
		*promptFlag = string(s.Config.UserName()) + "> "
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

	words, err := splitLine(line)
	if err != nil {
		fmt.Fprintf(s.Stderr, "upspin: shell: %s\n", err.Error())
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

// Split string line by space, preserving quoted tokens in an argv fashion.
// Allows for escaped and quotes in quote filenames.
// https://godoc.org/github.com/mgutz/str#ToArgv
func splitLine(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("no line given")
	}

	const (
		InArg = iota
		InArgQuote
		OutOfArg
	)
	currentState := OutOfArg
	currentQuoteChar := "\x00" // to distinguish between ' and " quotations
	// this allows to use "foo'bar"
	currentArg := ""
	argv := make([]string, 0)

	strLen := len(s)
	for i := 0; i < strLen; i++ {
		c := s[i : i+1]

		if unicode.Is(unicode.Quotation_Mark, rune(c[0])) {
			switch currentState {
			case OutOfArg:
				currentArg = ""
				fallthrough
			case InArg:
				currentState = InArgQuote
				currentQuoteChar = c

			case InArgQuote:
				if c == currentQuoteChar {
					currentState = InArg
				} else {
					currentArg += c
				}
			}
		} else if unicode.Is(unicode.White_Space, rune(c[0])) {
			switch currentState {
			case InArg:
				argv = append(argv, currentArg)
				currentState = OutOfArg
			case InArgQuote:
				currentArg += c
			case OutOfArg:
				// nothing
			}
		} else if c == `\` {
			switch currentState {
			case OutOfArg:
				currentArg = ""
				currentState = InArg
				fallthrough
			case InArg:
				fallthrough
			case InArgQuote:
				if i == strLen-1 {
					if runtime.GOOS == "windows" {
						// just add \ to end for windows
						currentArg += c
					} else {
						return nil, fmt.Errorf("escape character at end string")
					}
				} else {
					if runtime.GOOS == "windows" {
						peek := s[i+1 : i+2]
						if peek != `"` {
							currentArg += c
						}
					} else {
						i++
						c = s[i : i+1]
						currentArg += c
					}
				}
			}
		} else {
			switch currentState {
			case InArg, InArgQuote:
				currentArg += c

			case OutOfArg:
				currentArg = ""
				currentArg += c
				currentState = InArg
			}
		}
	}

	if currentState == InArg {
		argv = append(argv, currentArg)
	} else if currentState == InArgQuote {
		return nil, fmt.Errorf("starting quote has no ending quote.")
	}

	return argv, nil
}
