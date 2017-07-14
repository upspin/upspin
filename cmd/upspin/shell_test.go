// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"testing"
)

func TestShellSplitLine(t *testing.T) {
	testFields := []struct {
		line     string
		expected []string
	}{
		{"cp -R pictures @/pictures", do("cp", "-R", "pictures", "@/pictures")},
		{`cp -R "music/jim's mix"                   @/pictures`, do("cp", "-R", `music/jim's mix`, "@/pictures")},
		{`mkdir bob@foo.org/home`, do("mkdir", "bob@foo.org/home")},
		{`rm "@/tmp/all          the_things.mp3"`, do("rm", `@/tmp/all          the_things.mp3`)},
		{`cp \'homecoming.png @/pics/3005/homecoming.png`, do("cp", `'homecoming.png`, `@/pics/3005/homecoming.png`)},
		{`link @/site/access.log @/log/access.log`, do("link", "@/site/access.log", "@/log/access.log")},
		{`share -q -fix '@/Friends\'_Cars'`, do("share", "-q", "-fix", `@/Friends'_Cars`)},
		{`link '@/site/bad access.log' "@/log/bad access.log"`, do("link", `@/site/bad access.log`, `@/log/bad access.log`)},
	}

	for _, tf := range testFields {
		got, err := splitLine(tf.line)
		if err != nil {
			t.Error(err)
			continue
		}

		if !reflect.DeepEqual(got, tf.expected) {
			t.Errorf("shell.splitLine(%#q) = %#q; want %#q", tf.line, got, tf.expected)
		}
	}

	// test errors
	lines := []string{
		``,                    // empty line
		`         `,           // empty non-trimmed line
		`share "@/myfile.log`, // no ending quote
		`get @/myfile.log\`,   // end escape char
		`get "@/sdfsdf\"`,     // escape quote acting as end quote

	}

	for _, line := range lines {
		_, err := splitLine(line)
		if err == nil {
			t.Errorf(`shell.splitLine(%s) = nil; want ERROR`, line)
			continue
		}
	}
}
