// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"

	"upspin.io/upspin"
)

// cmdTest describes one sequence of subcommands to be run in order.
// These are run by the callers of testCommands in cmd_test.go
// The tests must be run sequentially. They build state as they run.
type cmdTest struct {
	name  string          // The name of the test set. This appears in the -v output.
	user  upspin.UserName // The user to run this as.
	cmds  []string        // The subcommands to run.
	stdin string          // The text to provide to standard input.
	// post is run after the subcommands complete. It can be used to verify
	// correct state results from their execution.
	post func(t *testing.T, r *runner, c *cmdTest, stdout, stderr string)
}

// basicCmdTests exercises the basic upspin commands such as put, get, etc.
var basicCmdTests = []cmdTest{
	// A couple of basic checks, mostly to test the test scaffolding itself.
	{
		"user ann",
		ann,
		do("user"),
		"",
		expect("name: ann@example.com"),
	},
	{
		"user pat",
		pat,
		do("user"),
		"",
		expect("name: pat@example.com"),
	},
	{
		"make user root",
		ann,
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
		ann,
		do("ls ann@example.com/bar"),
		"",
		fail("item does not exist"),
	},
	// Now the tests proper. Build some state and use it.
	{
		"build directories",
		ann,
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
		ann,
		"@/Group/friends",
		"pat@example.com chris@example.com\n",
	),
	putFile(
		ann,
		"@/Friends/Access",
		"r,l: friends\n*:ann@example.com\n",
	),
	// Create and build a Public directory, but do it wrong first to check failure.
	{
		"prevent read:all after content",
		ann,
		do(
			"mkdir @/BadPublic @/BadPublic/Photo",
			"put @/BadPublic/Access",
		),
		"r,l: all\n*:ann@example.com\n",
		fail("cannot add \"read:all\""),
	},
	{
		"make public directory",
		ann,
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
		ann,
		"@/Friends/Photo/friends.jpg",
		"this is friends.jpg",
	),
	putFile(
		ann,
		"@/Private/Photo/private.jpg",
		"this is private.jpg",
	),
	putFile(
		ann,
		"@/Public/Photo/public.jpg",
		"this is public.jpg",
	),
	{
		"link to a file",
		ann,
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
		ann,
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
		ann,
		do(
			"mkdir @/a*b",
		),
		"",
		fail("no path matches"),
	},
	{
		"successful mkdir with glob char",
		ann,
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
