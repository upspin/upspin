// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"
)

// cmdTest describes one sequence of subcommands to be run in order.
// These are run by TestCommands in cmd_test.go
type cmdTest struct {
	name  string   // The name of the test set. This appears in the -v output.
	cmds  []string // The subcommands to run.
	stdin string   // The text to provide to standard input.
	// post is run after the subcommands complete. It can be used to verify
	// correct state results from their execution.
	post func(t *testing.T, r *runner, c *cmdTest, stdout, stderr string)
}

// These tests must be run sequentially. They build state as they run.
var cmdTests = []cmdTest{
	{
		"user",
		do("user"),
		"",
		expect("name: ann@example.com"),
	},
	{
		"make user root",
		do(
			"mkdir ann@example.com",
			"put ann@example.com/foo",
			"get @/foo",
			"ls -l @/foo",
		),
		"this is ann@example.com/foo\n",
		expect(
			"this is ann@example.com/foo\n",
			"ee", "1", "28", "remote", "\tann@example.com/foo",
		),
	},
	{
		"list nonexistent file",
		do("ls ann@example.com/bar"),
		"",
		fail("item does not exist"),
	},
}
