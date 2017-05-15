// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package shutdown

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

const timeout = 10 * time.Second

// TestShutdown launches a child process, sends it SIGTERM, and, by reading its
// standard output, checks that the process runs the required shutdown
// functions. It also checks that the process will be forced to exit if a
// timeout handler stalls.
func TestShutdown(t *testing.T) {
	if os.Getenv(shutdownEnv) == "true" {
		testShutdownChildProcess()
		return
	}

	t.Run("clean", func(t *testing.T) { testShutdown(t, true) })
	t.Run("messy", func(t *testing.T) { testShutdown(t, false) })
}

const (
	shutdownEnv     = "SHUTDOWN_CHILD_PROCESS"
	shutdownKillEnv = shutdownEnv + "_KILL"
)

var shutdownMessages = []string{
	"Hello",
	"How are you?",
	"Goodbye",
}

func testShutdown(t *testing.T, clean bool) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestShutdown$")
	cmd.Env = []string{shutdownEnv + "=true"}
	if !clean {
		cmd.Env = append(cmd.Env, shutdownKillEnv+"=true")
	}

	// Scan process output line by line.
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
	readErr := make(chan error, 1)
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
	case <-time.After(timeout):
		t.Fatal("timed out waiting for child process to say hello")
	}

	// Collect and compare the remaining output lines.
	waitErr := make(chan error, 1)
	go func() {
		for n := 1; n < len(shutdownMessages); n++ {
			if !clean && n == 2 {
				// In messy mode the second shutdown
				// handler will not run.
				break
			}
			if !out.Scan() {
				readErr <- fmt.Errorf("child output ended, expected more lines")
				return
			}
			if got, want := out.Text(), shutdownMessages[n]; got != want {
				readErr <- fmt.Errorf("child output line %q, want %q", got, want)
				return
			}
		}
		if out.Scan() {
			readErr <- fmt.Errorf("child output unexpected line %q", out.Text())
			return
		}
		readErr <- nil
		waitErr <- cmd.Wait()
	}()

	// Check that the output was what we expected.
	if err := <-readErr; err != nil {
		cmd.Process.Kill()
		t.Fatal(err)
	}

	// Check exit status.
	select {
	case err := <-waitErr:
		if err != nil && clean {
			t.Fatalf("child process exited with non-zero status: %v", err)
		} else if err == nil && !clean {
			t.Fatal("child process exited cleanly, want non-zero status")
		}
	case <-time.After(timeout):
		cmd.Process.Kill()
		t.Fatal("timed out waiting for child process to exit")
	}
}

func testShutdownChildProcess() {
	var kill chan bool
	if os.Getenv(shutdownKillEnv) == "true" {
		kill = make(chan bool)
		killSleep = func(time.Duration) {
			<-kill
		}
	}

	Handle(func() {
		fmt.Println(shutdownMessages[2])
	})

	Handle(func() {
		fmt.Println(shutdownMessages[1])
		if kill != nil {
			kill <- true
			select {} // Block forever, stalling Shutdown.
		}
	})

	fmt.Println(shutdownMessages[0])

	Now(0)

	// If for some reason Shutdown returns the test must time out.
	select {}
}
