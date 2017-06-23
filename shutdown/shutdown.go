// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package shutdown provides a mechanism for registering handlers to be called
// on process shutdown.
package shutdown // import "upspin.io/shutdown"

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"upspin.io/log"
)

// GracePeriod specifies the maximum amount of time during which all shutdown
// handlers must complete before the process forcibly exits.
const GracePeriod = 1 * time.Minute

// Handle registers the onShutdown function to be run when the system is being
// shut down. On shutdown, registered functions are run in last-in-first-out
// order. Handle may be called concurrently.
func Handle(onShutdown func()) {
	shutdown.mu.Lock()
	defer shutdown.mu.Unlock()

	shutdown.sequence = append(shutdown.sequence, onShutdown)
}

// Now calls all registered shutdown closures in last-in-first-out order and
// terminates the process with the given status code.
// It only executes once and guarantees termination within GracePeriod.
// Now may be called concurrently.
func Now(code int) {
	shutdown.once.Do(func() {
		log.Debug.Printf("shutdown: status code %d", code)

		// Ensure we terminate after a fixed amount of time.
		go func() {
			killSleep(GracePeriod)
			// Don't use log package here; it may have been flushed already.
			fmt.Fprintf(os.Stderr, "shutdown: %v elapsed since shutdown requested; exiting forcefully", GracePeriod)
			os.Exit(1)
		}()

		shutdown.mu.Lock() // No need to ever unlock.
		for i := len(shutdown.sequence) - 1; i >= 0; i-- {
			shutdown.sequence[i]()
		}

		os.Exit(code)
	})
}

// Testing hook.
var killSleep = time.Sleep

var shutdown struct {
	mu       sync.Mutex
	sequence []func()
	once     sync.Once
}

func init() {
	// Close the listener when a shutdown event happens.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, os.Interrupt)
	go func() {
		sig := <-c
		log.Error.Printf("shutdown: process received signal %v", sig)
		Now(1)
	}()

	// Register the log shutdown routine here, to avoid an import cycle.
	// Shutting down the logger is the last thing we do.
	Handle(log.Flush)
}
