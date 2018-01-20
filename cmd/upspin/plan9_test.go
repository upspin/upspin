// Copyright 2018 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build plan9

package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestFindUpspinBinaries(t *testing.T) {
	testFiles := []struct {
		n string
		b bool
	}{
		{"some/path/upspin-foo", true},
		{"some/path/upspin-unexec", false}, // Will be marked not executable.
		{"thirst/path/upspin-baz", true},
		{"fourth/path/upspin-qux", true},
		{"yet/another/upspin-foo", true},
	}
	tmpDir, err := ioutil.TempDir("", "upspin-binary-test-")
	if err != nil {
		t.Fatalf("could not create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	paths := map[string]bool{}
	for _, tf := range testFiles {
		d := filepath.Dir(filepath.Join(tmpDir, tf.n))
		paths[d] = true
		if err = os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("could not create %s: %v", d, err)
			continue
		}
		perm := os.FileMode(0700)
		if strings.HasSuffix(tf.n, "unexec") {
			perm = 0600
		}
		f, err := os.OpenFile(filepath.Join(tmpDir, tf.n), os.O_RDWR|os.O_CREATE|os.O_TRUNC, perm)
		if err != nil {
			t.Fatalf("could not create temporary file %s: %v", tf.n, err)
			continue
		}
		f.Close()
	}

	defer os.Setenv("path", os.Getenv("path"))
	var newPath string
	for k, _ := range paths {
		newPath += k + string(filepath.ListSeparator)
	}
	err = os.Setenv("path", newPath)
	if err != nil {
		t.Fatalf("could not set path: %v", err)
	}

	binaries := findUpspinBinaries()
	sort.Strings(binaries)
	wanted := "baz;foo;foo;qux"
	got := strings.Join(binaries, ";")
	if wanted != got {
		t.Fatalf("expected %q, got %q", wanted, got)
	}
}
