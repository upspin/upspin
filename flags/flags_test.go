// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flags

import (
	"fmt"
	"testing"
)

type globalTest struct {
	name  string
	value string
	usage string
}

var globalTests = []globalTest{
	{"blocksize", fmt.Sprint(BlockSize), "`size` of blocks when writing large files"},
	{"cachedir", CacheDir, "`directory` containing all file caches"},
	{"https", defaultHTTPSAddr, "`address` for incoming secure network connections"},
}

func TestGlobals(t *testing.T) {
	Parse() // Registers all flags.
	globals := Globals("test")
	// Spot check a few manually.
	for _, test := range globalTests {
		f := globals.Lookup(test.name)
		if f == nil {
			t.Fatalf("%q flag not found in globals", test.name)
		}
		if f.Value.String() != test.value {
			t.Fatalf("%q: expected %q; got %q", test.name, test.value, f.Value)
		}
		if f.DefValue != test.value {
			t.Fatalf("%q: expected default %q; got %q", test.name, test.value, f.DefValue)
		}
		if f.Usage != test.usage {
			t.Fatalf("%q: expected usage %q; got %q", test.name, test.usage, f.Usage)
		}
	}
}
