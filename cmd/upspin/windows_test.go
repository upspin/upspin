// +build windows

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
		{"some/path/upspin-foo.exe", true},
		{"some/path/upspin-bar.txt", false},
		{"thirst/path/upspin-baz.bat", true},
		{"fourth/path/upspin-qux.com", true},
		{"yet/another/upspin-foo.cmd", true},
	}
	tmpDir, err := ioutil.TempDir("", "upspin-binary-test-")
	if err != nil {
		t.Fatalf("could not create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	var paths []string
	for _, tf := range testFiles {
		d := filepath.Dir(filepath.Join(tmpDir, tf.n))
		paths = append(paths, d)
		if err = os.MkdirAll(d, 0700); err != nil {
			t.Errorf("could not create %s: %v", d, err)
			continue
		}
		f, err := os.Create(filepath.Join(tmpDir, tf.n))
		if err != nil {
			t.Errorf("could not create temporary file %s: %v", tf.n, err)
			continue
		}
		f.Close()
	}
	defer os.Setenv("PATH", os.Getenv("PATH"))
	err = os.Setenv("PATH", strings.Join(paths, string(filepath.ListSeparator)))
	if err != nil {
		t.Fatalf("could not set PATH: %v", err)
	}

	binaries := findUpspinBinaries()
	sort.Strings(binaries)
	wanted := "baz;foo;foo;qux"
	got := strings.Join(binaries, ";")
	if wanted != got {
		t.Fatalf("list of binaries wrong. wanted '%s', got '%s'", wanted, got)
	}
}
