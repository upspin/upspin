// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Flag helpers.

package subcmd

import (
	"flag"
	"fmt"
	"os"
)

// ParseFlags parses the flags in the command line arguments,
// according to those set in the flag set.
func (s *State) ParseFlags(fs *flag.FlagSet, args []string, help, usage string) {
	helpFlag := fs.Bool("help", false, "print more information about the command")
	usageFn := func() {
		fmt.Fprintf(s.Stderr, "Usage: upspin %s\n", usage)
		if *helpFlag {
			fmt.Fprintln(s.Stderr, help)
		}
		// How many flags?
		n := 0
		fs.VisitAll(func(*flag.Flag) { n++ })
		if n > 0 {
			fmt.Fprintf(s.Stderr, "Flags:\n")
			fs.PrintDefaults()
		}
		if s.Interactive {
			panic("exit")
		}
	}
	fs.Usage = usageFn
	err := fs.Parse(args)
	if err != nil {
		s.Exit(err)
	}
	if *helpFlag {
		fs.Usage()
		os.Exit(2)
	}
}

// IntFlag returns the value of the named integer flag in the flag set.
func IntFlag(fs *flag.FlagSet, name string) int {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int)
}

// BoolFlag returns the value of the named boolean flag in the flag set.
func BoolFlag(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}

// StringFlag returns the value of the named string flag in the flag set.
func StringFlag(fs *flag.FlagSet, name string) string {
	return fs.Lookup(name).Value.(flag.Getter).Get().(string)
}
