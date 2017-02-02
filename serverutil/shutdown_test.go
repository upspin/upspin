// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"upspin.io/log"
)

const magicPort = ":9781"

// Test strategy:
// Launch test binary, get the PID.
// Check that it's running.
// Send SIGTERM
// Wait termination.
// Check that it's no longer running.
// Check side-effects to prove the shutdown routines ran.
// Profit!
func TestShutdown(t *testing.T) {
	now := time.Now()

	err := exec.Command("go", "install", "upspin.io/serverutil/testshutdown").Run()
	if err != nil {
		t.Fatal(err)
	}

	exitCode := fmt.Sprintf("exiting-now-%d-%d-%d", now.Nanosecond(), now.Second(), os.Getpid())
	cmd := exec.Command("testshutdown", exitCode)
	f, err := ioutil.TempFile("", "testshutdown")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = f
	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the server is up by making it return a secret message.
	for tries := 0; ; tries++ {
		testData := fmt.Sprintf("%d:%d:%d", now.Minute(), now.Second(), now.Nanosecond())
		err = pingServer(t, testData)
		if err == nil {
			break
		}
		if err != nil && tries > 4 {
			t.Fatal(err)
		}
		log.Error.Print(err)
		time.Sleep(50 * time.Millisecond)
	}

	// Send SIGTERM.
	err = syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to exit
	err = cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}

	// Read and validate output.
	output := make([]byte, len(exitCode))
	_, err = f.ReadAt(output, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	os.Remove(f.Name())
	if string(output) != exitCode {
		t.Fatalf("Expected %s, got %d", exitCode, output)
	}
}

func pingServer(t *testing.T, ping string) error {
	resp, err := http.Get("http://localhost" + magicPort + "/echo?str=" + ping)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != ping {
		return fmt.Errorf("expected %q, got %q", ping, data)
	}
	return nil
}
