// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command testenv builds and runs inprocess key, store, and directory servers
// and provides an upspin shell acting as the test user bob@b.com.
package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if err := do(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func do() error {
	f, err := ioutil.TempFile("", "context")
	if err != nil {
		return err
	}
	ctxFile := f.Name()
	f.Close()
	defer os.Remove(ctxFile)

	tlsCertDir := filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata")
	user := "bob@b.com"
	secrets := filepath.Join(os.Getenv("GOPATH"), "src/upspin.io/key/testdata/bob")

	// Build.
	cmd := exec.Command("go", "install",
		"upspin.io/cmd/keyserver",
		"upspin.io/cmd/storeserver",
		"upspin.io/cmd/dirserver",
		"upspin.io/cmd/upspin",
	)
	cmd.Stdout = prefixWriter("build: ", os.Stdout)
	cmd.Stderr = prefixWriter("build: ", os.Stderr)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build error: %v", err)
	}

	// Start servers.

	port := 8000
	keyAddr := fmt.Sprintf("localhost:%d", port)
	key := exec.Command(
		"keyserver",
		"-context="+ctxFile,
		"-https="+keyAddr,
		"-addr="+keyAddr, // is this necessary?
		"-log=debug",
		"-testuser="+user,
	)
	key.Env = []string{
		"upspinusername=key@example.net",
		"upspinsecrets=none",
		"upspintlscerts=" + tlsCertDir,
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	key.Stdout = prefixWriter("keyserver:\t", os.Stdout)
	key.Stderr = prefixWriter("keyserver:\t", os.Stderr)
	if err := key.Start(); err != nil {
		return fmt.Errorf("starting keyserver: %v", err)
	}
	defer kill(key)

	port++
	storeAddr := fmt.Sprintf("localhost:%d", port)
	store := exec.Command(
		"storeserver",
		"-context="+ctxFile,
		"-https="+storeAddr,
		"-addr="+storeAddr, // is this necessary?
		"-log=debug",
		// -testuser ?
	)
	store.Env = []string{
		"upspinusername=store@example.net",
		"upspinkeyserver=remote," + keyAddr,
		"upspinsecrets=none",
		"upspintlscerts=" + tlsCertDir,
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	store.Stdout = prefixWriter("storeserver:\t", os.Stdout)
	store.Stderr = prefixWriter("storeserver:\t", os.Stderr)
	if err := store.Start(); err != nil {
		return fmt.Errorf("starting storeserver: %v", err)
	}
	defer kill(store)

	port++
	dirAddr := fmt.Sprintf("localhost:%d", port)
	dir := exec.Command(
		"dirserver",
		"-context="+ctxFile,
		"-https="+dirAddr,
		"-addr="+dirAddr, // is this necessary?
		"-log=debug",
		// -testuser ?
	)
	dir.Env = []string{
		"upspinusername=" + user,
		"upspinkeyserver=remote," + keyAddr,
		"upspinstoreserver=remote," + storeAddr,
		"upspinsecrets=" + secrets,
		"upspintlscerts=" + tlsCertDir,
		"upspinpacking=ee",
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	dir.Stdout = prefixWriter("dirserver:\t", os.Stdout)
	dir.Stderr = prefixWriter("dirserver:\t", os.Stderr)
	if err := dir.Start(); err != nil {
		return fmt.Errorf("starting dirserver: %v", err)
	}
	defer kill(dir)

	shell := exec.Command(
		"upspin",
		"-context="+ctxFile,
		"-log=debug",
		"shell",
	)
	shell.Stdin = os.Stdin
	shell.Stdout = os.Stdout
	shell.Stderr = os.Stderr
	shell.Env = append(
		os.Environ(),
		"upspinusername="+user,
		"upspinkeyserver=remote,"+keyAddr,
		"upspinstoreserver=remote,"+storeAddr,
		"upspindirserver=remote,"+dirAddr,
		"upspinsecrets="+secrets,
		"upspinpacking=ee",
		"upspintlscerts="+tlsCertDir,
	)
	return shell.Run()
}

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

func prefixWriter(prefix string, out io.Writer) io.Writer {
	r, w := io.Pipe()
	go func() {
		s := bufio.NewScanner(r)
		for s.Scan() {
			fmt.Fprintf(out, "%s%s\n", prefix, s.Bytes())
		}
	}()
	return w
}
