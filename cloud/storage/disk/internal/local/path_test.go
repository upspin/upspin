// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package local

import (
	"path/filepath"
	"strings"
	"testing"
)

type pathTest struct {
	ref  string
	file string
}

var pathTests = []pathTest{
	// The values are base64.URLEncoded; we care about the structure
	// especially for corner cases. Final path element will always be
	// exactly one byte or more than two.
	{"", "B/++/+"},
	{"x", "B/eA/+"},
	{"xx", "B/eH/g"},
	{"xxx", "B/eH/h4/+"},
	{"xxxx", "B/eH/h4/eA/+"},
	{"xxxxx", "B/eH/h4/eH/g"},
	{"0123456", "B/MD/Ey/Mz/Q1/Ng/+"},
	{"01234567", "B/MD/Ey/Mz/Q1/Nj/c"},
	{"012345678", "B/MD/Ey/Mz/Q1/Nj/c4"}, // No padding needed after 5 dirs.
	{"0123456789", "B/MD/Ey/Mz/Q1/Nj/c4OQ"},
	{"qwertyuiopasdfghjklzxcvbnm", "B/cX/dl/cn/R5/dW/lvcGFzZGZnaGprbHp4Y3Zibm0"},
	// Verify safety of references with metacharacters.
	{"/", "B/Lw/+"},
	{"/dev/disk", "B/L2/Rl/di/9k/aX/Nr"},
}

func TestPath(t *testing.T) {
	for _, test := range pathTests {
		got := Path("B", test.ref)
		// Convert from a local path name, so test is not Unix-specific.
		got = filepath.ToSlash(got)
		if got != test.file {
			t.Errorf("Path(%q) = %q; expected %q", test.ref, got, test.file)
		}
	}
}

func TestRef(t *testing.T) {
	for _, test := range pathTests {
		// Convert to a local path name, so test is not Unix-specific.
		file := filepath.FromSlash(test.file)
		got, err := Ref(strings.TrimPrefix(file, "B/"))
		if err != nil {
			t.Errorf("Ref(%q): %v", file, err)
			continue
		}
		if got != test.ref {
			t.Errorf("Ref(%q) = %q; expected %q", file, got, test.ref)
		}
	}
}
