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
		"config",
		ann,
		do("config"),
		"",
		expect("username: ann@example.com", "secrets", "packing: ee", "storeserver:", "dirserver:", "keyserver"),
	},
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
			"ee", "2", "28", "remote", "\tann@example.com/foo",
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
			"mkdir -p @/Friends/Photo",
			"mkdir -p @/Private/Photo",
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
		"@/Group/Access",
		"r:*@example.com\n",
	),
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
	// Create and build a Public directory.
	{
		"make public directory",
		ann,
		do(
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
		"info Friends",
		ann,
		do(
			"info @/Friends",
		),
		"",
		expect(
			"ann@example.com/Friends",
			"packing:", "ee",
			"writer:", "dirserver@example.com",
			"attributes:", "directory",
			"key holders:", "is a directory",
			"can read:", "ann@example.com chris@example.com",
			"can write:", "ann@example.com",
			"can list:", "ann@example.com chris@example.com",
			"can create:", "ann@example.com",
			"can delete:", "(same)",
		),
	},
	{
		"info link",
		ann,
		do(
			"info @/linkdir",
		),
		"",
		expect(
			"ann@example.com/linkdir",
			"attributes:", "link",
			"access file:", "ann@example.com/Public/Access",
			"key holders:", "all@upspin.io ann@example.com",
			"Target of link", "ann@example.com/linkdir:",
			"ann@example.com/Public/Photo",
		),
	},
	{
		"info -R",
		ann,
		do(
			"info -R @/Friends",
		),
		"",
		expect(
			"\nann@example.com/Friends\n", // Each file info starts with the file name on a line.
			"\nann@example.com/Friends/Access\n",
			"\nann@example.com/Friends/Photo\n",
			"\nann@example.com/Friends/Photo/friends.jpg\n",
		),
	},
	{
		"put of plain file",
		ann,
		do(
			"put -packing plain @/justtext",
			"info @/justtext",
			"get @/justtext",
		),
		"some stuff to save",
		expect(
			"packing:", "plain",
			"some stuff to save",
		),
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
			"ls @+snapshot",
		),
		"",
		fail("item does not exist"),
	},
	{
		"create snapshot",
		ann,
		do(
			"snapshot",
			"ls @+snapshot",
		),
		"",
		expect("ann+snapshot@example.com/2"), // "/2" for "/2017" - or maybe later.
	},
	{
		"info on public file",
		ann,
		do(
			"info @/Public/Photo/public.jpg",
		),
		"",
		expect(
			"ann@example.com/Public/Photo/public.jpg",
			"packing:", "ee",
			"writer:", "ann@example.com",
			"key holders:", "all@upspin.io ann@example.com",
			"can read:", "All ann@example.com",
			"can write:", "ann@example.com",
			"can list:", "All ann@example.com",
			"can create:", "ann@example.com",
			"can delete:", "(same)",
		),
	},
	{
		"make public directory private",
		ann,
		do(
			"put @/Public/Access",
			"share -q -fix -r @/Public",
			"info @/Public/Photo/public.jpg",
		),
		"*:ann@example.com\n",
		expect(
			"ann@example.com/Public/Photo/public.jpg",
			"packing:", "ee",
			"writer:", "ann@example.com",
			"key holders:", "ann@example.com",
			"can read:", "(same)",
			"can write:", "(same)",
			"can list:", "(same)",
			"can create:", "(same)",
			"can delete:", "(same)",
		),
	},
	{
		"check access file still public",
		ann,
		do(
			"info @/Public/Access",
			"put @/Public/Access",
		),
		"r,l: all\n*:ann@example.com\n",
		expect(
			"ann@example.com/Public/Access",
			"packing:", "ee",
			"writer:", "ann@example.com",
			"key holders:", "all@upspin.io ann@example.com",
			"can read:", "ann@example.com",
			"can write:", "(same)",
			"can list:", "(same)",
			"can create:", "(same)",
			"can delete:", "(same)",
		),
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

// cpTests tests the cp command. There are four basic cases:
// upspin to Upspin, local to Upspin, Upspin to local, and local to local.
var cpTests = []cmdTest{
	// Build a little local tree.
	{
		"build tree to cp, part 1",
		ann,
		do(
			"mkdir @/cp",
			"put @/cp/file",
		),
		"this is @/cp/file",
		expectNoOutput(),
	},
	{
		"build tree to cp, part 2",
		ann,
		do(
			"mkdir @/cp/subdir",
			"put @/cp/subdir/file",
		),
		"this is @/cp/subdir/file",
		expectNoOutput(),
	},
	// Now tests.
	{
		"cannot cp to non-existent directory",
		ann,
		do(
			"cp -R @/cp @/cp2",
		),
		"",
		fail("existing directory"),
	},
	// Copy tree recursively four ways.
	{
		"cp Upspin to Upspin",
		ann,
		do(
			"mkdir @/cp2",
			"cp -R @/cp/* @/cp2",
			"get @/cp2/file",
			"get @/cp2/subdir/file",
		),
		"",
		expect("this is @/cp/file", "this is @/cp/subdir/file"),
	},
	{
		// Do the rest in one big hit so we test all cases but can use get
		// for the final check.
		"cp Upspin to local and back",
		ann,
		do(
			"mkdir @/cp3",
			"cp -R @/cp/* "+testTempDir("cp", deleteOld),
			"cp -R "+testTempGlob("cp")+" "+testTempDir("cp2", deleteOld),
			"cp -R "+testTempGlob("cp2")+" @/cp3",
			"get @/cp3/file",
			"get @/cp3/subdir/file",
		),
		"",
		expect("this is @/cp/file", "this is @/cp/subdir/file"),
	},
	{
		"cp without overwrite",
		ann,
		do(
			"put @/cp/file",
			"rm @/cp/subdir/file",
			"cp -R -overwrite=false "+testTempGlob("cp")+" @/cp",
			"get @/cp/file",
			"get @/cp/subdir/file",
		),
		"this is @/cp/file new content",
		expect("this is @/cp/file new content", "this is @/cp/subdir/file"),
	},
}

// lsTests tests the ls command, in particular its handling of links.
// See issue 510.
var lsTests = []cmdTest{
	{
		"create links",
		ann,
		do(
			"mkdir @/linktest",
			"put @/linktest/file",
			"link @/linktest/file @/linktest/link",
		),
		"a linked-to-file",
		expectNoOutput(),
	},
	{
		"ls links",
		ann,
		do(
			"ls @/linktest/file",
			"ls @/linktest/link",
			"ls -L @/linktest/link",
		),
		"",
		expect(
			"ann@example.com/linktest/file",
			"ann@example.com/linktest/link",
			"ann@example.com/linktest/file",
		),
	},
	{
		"ls links with wildcards",
		ann,
		do(
			"ls @/link?est/l?nk",
			"ls @/linktest/l?nk",
			"ls @/link?est/link",
		),
		"",
		expect(
			"ann@example.com/linktest/link",
			"ann@example.com/linktest/link",
			"ann@example.com/linktest/link",
		),
	},
	{
		"ls -L links with wildcards",
		ann,
		do(
			"ls -L @/link?est/l?nk",
			"ls -L @/linktest/l?nk",
			"ls -L @/link?est/link",
		),
		"",
		expect(
			"ann@example.com/linktest/file",
			"ann@example.com/linktest/file",
			"ann@example.com/linktest/file",
		),
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
		expectNoOutput(),
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
		expectNoOutput(),
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

// The keygen tests update the keys for the user. Since the command test reloads the
// environment for each cmdTest, we can also test that the new keys work.
var keygenTests = []cmdTest{
	{
		"create a temporary key",
		ann,
		do(
			"keygen -secretseed disis-valid-fosoh-matij.disis-valid-fosoh-matij " + testTempDir("key", deleteOld),
		),
		"",
		keygenVerify(testTempDir("key", keepOld), "p256\n24759220094", "3291910954783938", "", keepOld),
	},
	{
		"keygen again will fail",
		ann,
		do(
			"keygen -secretseed bonus-favor-panat-fakir.kolor-kivil-koral-hovit " + testTempDir("key", keepOld),
		),
		"",
		fail("prior keys exist"),
	},
	{
		"keygen rotate",
		ann,
		do(
			"keygen -rotate -secretseed bonus-favor-panat-fakir.kolor-kivil-koral-hovit " + testTempDir("key", keepOld),
		),
		"",
		keygenVerify(testTempDir("key", keepOld), "p256\n33850756267", "1135817957601671", "3291910954783938", deleteOld),
	},
	{
		"use new keys",
		ann,
		do(
			"mkdir @/keytest",
			"ls @/keytest",
			"rm @/keytest",
		),
		"",
		expectNoOutput(),
	},
}

// The suffixed user tests create a new suffixed user confirming that the
// config and key files for that user are created and that the user is known
// to the key server. They also confirm that a suffixed user can not create
// a suffixed user.
var suffixedUserTests = []cmdTest{
	{
		"create a suffixed user",
		ann,
		do(
			"createsuffixeduser -secrets=" + testTempDir("key", deleteOld) + " ann+quux@example.com",
		),
		"",
		suffixedUserExists("ann", "quux"),
	},
	{
		"user ann+quux",
		ann,
		do("user ann+quux@example.com"),
		"",
		expect("name: ann+quux@example.com", "dirs", "- remote,localhost", "stores", "- remote,localhost", "publickey"),
	},
}
