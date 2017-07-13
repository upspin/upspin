// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"
)

// cmdTest describes one sequence of subcommands to be run in order.
// These are run by the callers of testCommands in cmd_test.go
// The tests must be run sequentially. They build state as they run.
type cmdTest struct {
	name  string   // The name of the test set. This appears in the -v output.
	cmds  []string // The subcommands to run.
	stdin string   // The text to provide to standard input.
	// post is run after the subcommands complete. It can be used to verify
	// correct state results from their execution.
	post func(t *testing.T, r *runner, c *cmdTest, stdout, stderr string)
}

// basicCmdTests exercises the basic upspin commands such as put, get, etc.
var basicCmdTests = []cmdTest{
	// A couple of basic checks, mostly to test the test scaffolding itself.
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
			"rm ann@example.com/foo", // Clean up afterwards.
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
	// Now the tests proper. Build some state and use it.
	{
		"build directories",
		do(
			"mkdir @/Group",
			"mkdir @/Friends @/Friends/Photo",
			"mkdir @/Private @/Private/Photo",
			"ls -R",
		),
		"",
		expect(
			// ls output is sorted and breadth-first.
			"Friends",
			"Group",
			"Private",
			"Friends/Photo",
			"Private/Photo",
		),
	},
	putFile(
		"@/Group/friends",
		"pat@example.com chris@example.com\n",
	),
	putFile(
		"@/Friends/Access",
		"r,l: friends\n*:ann@example.com\n",
	),
	// Create and build a Public directory, but do it wrong first to check failure.
	{
		"prevent read:all after content",
		do(
			"mkdir @/BadPublic @/BadPublic/Photo",
			"put @/BadPublic/Access",
		),
		"r,l: all\n*:ann@example.com\n",
		fail("cannot add \"read:all\""),
	},
	{
		"make public directory",
		do(
			"rm -R @/BadPublic",
			"mkdir @/Public",
			"put @/Public/Access",
			"get @/Public/Access",
			"mkdir @/Public/Photo",
		),
		"r,l: all\n*:ann@example.com\n",
		expect("r,l: all\n*:ann@example.com\n"),
	},
	putFile(
		"@/Friends/Photo/friends.jpg",
		"this is friends.jpg",
	),
	putFile(
		"@/Private/Photo/private.jpg",
		"this is private.jpg",
	),
	putFile(
		"@/Public/Photo/public.jpg",
		"this is public.jpg",
	),
	{
		"link to a file",
		do(
			"link @/Public/Photo/public.jpg @/tmp.jpg",
			"get @/tmp.jpg",
			"rm @/tmp.jpg",
		),
		"",
		expect("this is public.jpg"),
	},
	{
		"link to a directory",
		do(
			"link @/Public/Photo @/tmpdir",
			"get @/tmpdir/public.jpg",
			"rm @/tmpdir",
		),
		"",
		expect("this is public.jpg"),
	},
}

// globTests tests glob processing, and the ability to disable it.
// TODO: Test lots more.
var globTests = []cmdTest{
	// Verify that Glob processing can be disabled.
	{
		"erroneous mkdir with glob char",
		do(
			"mkdir @/a*b",
		),
		"",
		fail("no path matches"),
	},
	{
		"successful mkdir with glob char",
		do(
			"mkdir -glob=false @/a[1]b",
			"mkdir -glob=false @/a[1]b/c**d",
			"put -glob=false @/a[1]b/c**d/file1",
			"get @/a?1?b/c??d/file1", // Note: globbing enabled here.
			"rm -R -glob=false @/a[1]b",
		),
		"text of file1",
		expect("text of file1"),
	},
}
