// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestShutdown launches a child process, sends it SIGTERM, and, by reading its
// standard output, checks that the process runs the required shutdown
// functions. It also checks that the process will be forced to exit if a
// timeout handler stalls.
func TestShutdown(t *testing.T) {
	const key = "SHUTDOWN_CHILD_PROCESS"
	if os.Getenv(key) == "true" {
		testShutdownChildProcess()
		return
	}

	t.Run("clean", func(t *testing.T) { testShutdown(t, true) })
	t.Run("messy", func(t *testing.T) { testShutdown(t, false) })
}

var shutdownMessages = []string{
	"Hello",
	"How are you?",
	"Goodbye",
}

func testShutdown(t *testing.T, clean bool) {
	cmd := exec.Command(os.Args[0], "-test.run=TestShutdown")
	cmd.Env = []string{"SHUTDOWN_CHILD_PROCESS=true"}
	if !clean {
		cmd.Env = append(cmd.Env, "SHUTDOWN_TIMEOUT=true")
	}
	rc, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	out := bufio.NewScanner(rc)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Check that we get the initial "hello" message from the client,
	// so we know it's running.
	readErr := make(chan error)
	go func() {
		out.Scan()
		if err := out.Err(); err != nil {
			readErr <- err
			return
		}
		if got, want := out.Text(), shutdownMessages[0]; got != want {
			readErr <- fmt.Errorf("child said %q, want %q", got, want)
			return
		}
		readErr <- nil
	}()
	select {
	case err := <-readErr:
		if err != nil {
			cmd.Process.Kill()
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for child process to say hello")
	}

	// Collect and compare the remaining output lines.
	go func() {
		for n := 1; n < len(shutdownMessages); n++ {
			if !out.Scan() {
				readErr <- fmt.Errorf("child output ended, expected more lines")
				return
			}
			if got, want := out.Text(), shutdownMessages[n]; got != want {
				readErr <- fmt.Errorf("got output line %q, want %q", got, want)
				return
			}
		}
		readErr <- nil
	}()

	// Kill the process and wait for it to exit, checking its exit status
	// depending on whether this is a clean or messy text.
	if err := syscall.Kill(cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitErr := make(chan error)
	go func() {
		waitErr <- cmd.Wait()
	}()
	select {
	case err := <-waitErr:
		if err != nil && clean {
			t.Fatalf("child process exited with non-zero status: %v", err)
		} else if err == nil && !clean {
			t.Fatal("child proces exited cleanly, want non-zero status")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for child process to exit")
	}

	// Check that the output was what we expected.
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
}

func testShutdownChildProcess() {
	var terminate chan bool
	if os.Getenv("SHUTDOWN_TIMEOUT") == "true" {
		terminate = make(chan bool)
		terminateSleep = func(time.Duration) {
			<-terminate
		}
	}
	fmt.Println(shutdownMessages[0])
	RegisterShutdown(0, func() {
		fmt.Println(shutdownMessages[1])
	})
	RegisterShutdown(1, func() {
		fmt.Println(shutdownMessages[2])
		if terminate != nil {
			terminate <- true
			select {} // Block forever.
		}
	})
	Shutdown()
}
