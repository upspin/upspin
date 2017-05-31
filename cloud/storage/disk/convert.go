// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// The convert command is a tool to convert a local file tree used for on-disk
// storage, as constructed by cloud/storage/disk, from the old file name mapping
// to the new.
// Run it like this:
//	go run convert.go -old OLDTREE -new NEWTREE
// where OLDTREE is the base of an existing storage tree as created by disk.New
// using the old path encoding, and NEWTREE is an empty (or to-be-created) tree
// where the data will be written using the new encoding. OLDTREE and NEWTREE
// must be distinct directories.
//
// For details about the encodings see
//	upspin.io/cloud/storage/disk/internal/local/path.go.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/cloud/storage/disk/internal/local"
)

var (
	oldDir = flag.String("old", "", "directory of existing old tree; must be set")
	newDir = flag.String("new", "", "directory of empty new tree; must be set")
)

func main() {
	log.SetFlags(log.Lshortfile)
	log.SetPrefix("")
	flag.Parse()
	if *oldDir == "" || *newDir == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *oldDir == *newDir {
		log.Fatal("new and old directories must be distinct")
	}
	if empty(*oldDir) {
		log.Fatal("old directory does not exist or is empty")
	}
	if !empty(*newDir) {
		log.Fatal("new directory is not empty")
	}
	// Create a marker directory to signify that this tree uses the new encoding.
	// This is an actual directory that may eventually hold information but
	// whose name can only exist if the tree uses the new encoding.
	if err := os.MkdirAll(filepath.Join(*newDir, "++"), 0700); err != nil {
		log.Fatal(err)
	}
	err := filepath.Walk(*oldDir, walk)
	if err != nil {
		log.Fatal(err)
	}
}

// empty reports whether the directory is non-existent or empty.
func empty(dir string) bool {
	fd, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true
		}
		log.Fatal(err)
	}
	defer fd.Close()
	names, err := fd.Readdirnames(0)
	if err != nil {
		log.Fatal(err)
	}
	return len(names) == 0
}

// Walk walks the tree converting from the old references to the new.
func walk(path string, info os.FileInfo, err error) error {
	if err != nil {
		log.Fatal(err)
	}
	switch {
	case info.Mode().IsDir():
		return nil
	case info.Mode().IsRegular():
		return copy(path)
	}
	return fmt.Errorf("%q is not a directory or regular file", path)
}

// copy copies the contents of path to a directory under newDir,
// updating the reference by decoding the old base64 encoding.
func copy(old string) error {
	// First we recreate the base64 encoding from the old file name.
	if !strings.HasPrefix(old, *oldDir) {
		return fmt.Errorf("old path %q is not in old directory - cannot happen", old)
	}
	enc := old[len(*oldDir):]
	if len(enc) < 3 {
		return nil //  Must be the root
	}
	if enc[0] != os.PathSeparator || enc[3] != os.PathSeparator { // Path starts "/xx/"
		log.Printf("old path %q is in unrecognized format; ignoring", old)
		return nil
	}
	enc = enc[4:] // Drop the "/xx/" part. The rest is the base64 encoding.
	oldRef, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return fmt.Errorf("old path %q is invalid base 64 encoding: %v", old, err)
	}
	// ref is the decoded reference. Re-encode it with the new path encoding.
	newPath := local.Path(*newDir, string(oldRef))
	contents, err := ioutil.ReadFile(old)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
		return err
	}
	return ioutil.WriteFile(newPath, contents, 0600)
}
