// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// NOTE:
// Because testutil is invoked by tests throughout the Upspin tree,
// it cannot import any Upspin packages.

// Package testutil includes utility functions for Upspin tests.
package testutil // import "upspin.io/test/testutil"

import (
	"go/build"
	"log"
	"net"
	"os"
	"path/filepath"
)

// Repo returns the local filename of a file in the Upspin repository.
func Repo(dir ...string) string {
	wd, _ := os.Getwd()
	p, err := build.Import("upspin.io/upspin", wd, build.FindOnly)
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(filepath.Dir(p.Dir), filepath.Join(dir...))
}

// PickPort listens to an available port on localhost, closes the listener, and
// returns the port number. This function may be used for finding an available
// port for tests that use the network.
func PickPort() (string, error) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return "", err
	}
	return port, err
}
