// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"

	"upspin.io/upspin"
)

const (
	deleteOld = false
	keepOld   = true
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
		"user chris",
		chris,
		do("user"),
		"",
		expect("name: chris@example.com"),
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
		"chris@example.com\n", // We will add kelly@ in the share test.
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
			"link @/Public/Photo @/linkdir",
			"get @/linkdir/public.jpg",
		),
		"",
		expect("this is public.jpg"),
	},
	{
		"whichaccess",
		ann,
		do(
			"whichaccess @/",
			"whichaccess @/Group",
			"whichaccess @/Public/Photo",
			"whichaccess @/Public/Photo/public.jpg",
			"whichaccess @/linkdir",
			"whichaccess @/linkdir/public.jpg",
		),
		"",
		expect(
			"owner only",
			"owner only",
			"/Public/Access",
			"/Public/Access",
			"/Public/Access",
			"/Public/Access",
		),
	},
	{
		"no snapshot yet",
		ann,
		do(
			"ls ann+snapshot@example.com", // TODO: Use @+ when available.
		),
		"",
		fail("item does not exist"),
	},
	{
		"create snapshot",
		ann,
		do(
			"snapshot",
		),
		"",
		// TODO: Because of #428, can't do this directly.
		snapshotVerify(),
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

// shareTests tests share processing,.
// TODO: Test lots more.
var shareTests = []cmdTest{
	// Make sure that kelly@ cannot read the Friends directory.
	{
		"kelly can't read friends yet",
		kelly,
		do(
			"get ann@example.com/Friends/Photo/friends.jpg",
		),
		"",
		fail("information withheld"),
	},
	// Add kelly@ to the friends group.
	putFile(
		ann,
		"@/Group/friends",
		"chris@example.com kelly@example.com\n",
	),
	// kelly@ still can't read it (although this might be fixed one day).
	{
		"kelly can't read friends.jpg yet",
		kelly,
		do(
			"get ann@example.com/Friends/Photo/friends.jpg",
		),
		"",
		fail("no wrapped key for user"),
	},
	// Do the share; that should fix it.
	{
		"ann shares @/Friends",
		ann,
		do(
			"share -q -fix -r @/Friends",
		),
		"",
		expect(""),
	},
	// Now kelly@ can read it.
	{
		"kelly can read friends.jpg now",
		kelly,
		do(
			"get ann@example.com/Friends/Photo/friends.jpg",
		),
		"",
		expect("this is friends.jpg"),
	},
	// Now a similar dance but using an Access file and new reader lee@.
	// Make sure that lee@ cannot read the Friends directory.
	{
		"lee can't read friends yet",
		lee,
		do(
			"get ann@example.com/Friends/Photo/friends.jpg",
		),
		"",
		fail("information withheld"),
	},
	// Add lee@ to the Access file.
	putFile(
		ann,
		"@/Friends/Access",
		"r,l: friends lee@example.com\n*:ann@example.com\n",
	),
	// lee@ still can't read it (although this might be fixed one day).
	{
		"lee can't read friends.jpg yet",
		lee,
		do(
			"get ann@example.com/Friends/Photo/friends.jpg",
		),
		"",
		fail("no wrapped key for user"),
	},
	// Do the share; that should fix it.
	{
		"ann shares @/Friends (2)",
		ann,
		do(
			"share -q -fix -r @/Friends",
		),
		"",
		expect(""),
	},
	// Now lee@ can read it.
	{
		"lee can read friends.jpg now",
		lee,
		do(
			"get ann@example.com/Friends/Photo/friends.jpg",
		),
		"",
		expect("this is friends.jpg"),
	},
}

// keygenTests involves a user (keyloser@) whose only purpose is this test, because
// when we are done we have rotated the user's keys but not updated the keyserver.
// We can't use ann@ because we don't know her proquint so we can't restore.
var keygenTests = []cmdTest{
	{
		"create a temporary key",
		keyloser,
		do(
			"keygen -secretseed deter-gonad-pivot-rotor.visit-roman-widow-woman -where " + testTempDir("key", deleteOld),
		),
		"",
		keygenVerify(testTempDir("key", keepOld), "p256\n3078263077187835", "1623258616618034", "", keepOld),
	},
	{
		"keygen again will fail",
		keyloser,
		do(
			"keygen -secretseed desex-fetid-pecan-fakir.color-civil-comet-haven -where " + testTempDir("key", keepOld),
		),
		"",
		fail("prior keys exist"),
	},
	{
		"keygen rotate",
		keyloser,
		do(
			"keygen -rotate -secretseed desex-fetid-pecan-fakir.color-civil-comet-haven -where " + testTempDir("key", keepOld),
		),
		"",
		keygenVerify(testTempDir("key", keepOld), "p256\n1048813400173469", "7863414033373202", "1623258616618034", deleteOld),
	},
}
