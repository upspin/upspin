// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upbox builds and runs inprocess key, store, and directory servers
// and provides an upspin shell acting as the test user bob@b.com.
package main

// TODO(adg): wait for each server to start up
// TODO(adg): take username as an argument

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	logLevel = flag.String("log", "info", "log `level`")
	basePort = flag.Int("port", 8000, "base `port` number for upspin servers")
)

func main() {
	flag.Parse()
	if err := do(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func do() error {
	tmpDir, err := ioutil.TempDir("", "upbox")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := generateCert(tmpDir); err != nil {
		return err
	}

	port := *basePort
	keyAddr := fmt.Sprintf("localhost:%d", port)
	port++
	storeAddr := fmt.Sprintf("localhost:%d", port)
	port++
	dirAddr := fmt.Sprintf("localhost:%d", port)

	user := "bob@b.com"

	secrets := filepath.Join(os.Getenv("GOPATH"), "src/upspin.io/key/testdata/bob")
	if err := checkExists(secrets); err != nil {
		return err
	}

	rcFile := filepath.Join(tmpDir, "rc")
	rcContent := strings.Join([]string{
		"username=" + user,
		"keyserver=remote," + keyAddr,
		"storeserver=remote," + storeAddr,
		"dirserver=remote," + dirAddr,
		"secrets=" + secrets,
		"packing=ee",
		"tlscerts=" + tmpDir,
	}, "\n")
	if err := ioutil.WriteFile(rcFile, []byte(rcContent), 0644); err != nil {
		return err
	}

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

	server := func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(name, append([]string{
			"-context=" + rcFile,
			"-log=" + *logLevel,
			"-tls_cert=" + filepath.Join(tmpDir, "cert.pem"),
			"-tls_key=" + filepath.Join(tmpDir, "key.pem"),
		}, args...)...)
		cmd.Stdout = prefixWriter(name+":\t", os.Stdout)
		cmd.Stderr = prefixWriter(name+":\t", os.Stderr)
		return cmd
	}

	// Start keyserver.
	key := server("keyserver",
		"-https="+keyAddr,
		"-addr="+keyAddr, // is this necessary?
		"-testuser="+user,
	)
	key.Env = []string{
		"upspinusername=key@example.net",
		"upspinsecrets=none",
		"upspinpacking=plain",
		"upspinstoreserver=unassigned,",
		"upspindirserver=unassigned,",
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	if err := key.Start(); err != nil {
		return fmt.Errorf("starting keyserver: %v", err)
	}
	defer kill(key)

	// Start storeserver.
	store := server("storeserver",
		"-https="+storeAddr,
		"-addr="+storeAddr, // is this necessary?
	)
	store.Env = []string{
		"upspinusername=store@example.net",
		"upspinsecrets=none",
		"upspinpacking=plain",
		"upspinstoreserver=inprocess,",
		"upspindirserver=unassigned,",
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	if err := store.Start(); err != nil {
		return fmt.Errorf("starting storeserver: %v", err)
	}
	defer kill(store)

	// Start dirserver.
	dir := server("dirserver",
		"-https="+dirAddr,
		"-addr="+dirAddr, // is this necessary?
	)
	dir.Env = []string{
		"upspindirserver=inprocess,",
		"GOPATH=" + os.Getenv("GOPATH"),
	}
	if err := dir.Start(); err != nil {
		return fmt.Errorf("starting dirserver: %v", err)
	}
	defer kill(dir)

	// Start upspin shell.
	shell := exec.Command("upspin",
		"-context="+rcFile,
		"-log="+*logLevel,
		"shell",
	)
	shell.Stdin = os.Stdin
	shell.Stdout = os.Stdout
	shell.Stderr = os.Stderr
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

func checkExists(path ...string) error {
	for _, p := range path {
		_, err := os.Stat(p)
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist: %v", err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
