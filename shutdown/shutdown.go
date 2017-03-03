// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package shutdown provides a mechanism for registering handlers to be called
// on process shutdown.
package shutdown // import "upspin.io/shutdown"

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"upspin.io/log"
)

// Handle registers the onShutdown function to be run when the system is being
// shut down. On shutdown, registered functions are run in LIFO order.
// Handle may be called concurrently.
func Handle(onShutdown func()) {
	shutdown.mu.Lock()
	defer shutdown.mu.Unlock()

	shutdown.sequence = append(shutdown.sequence, onShutdown)
}

// Now calls all registered shutdown closures in last-in-first-out order
// and terminates. It only executes once and guarantees termination within a
// bounded amount of time. It's safe for many goroutines to call Now.
func Now() {
	const op = "shutdown.Now"

	shutdown.once.Do(func() {
		log.Info.Printf("%s: Shutdown requested", op)

		shutdown.mu.Lock()

		// Ensure we terminate after a fixed amount of time.
		go func() {
			killSleep(1 * time.Minute)
			log.Error.Printf("%s: Clean shutdown stalled; forcing shutdown", op)
			os.Exit(1)
		}()

		for i := len(shutdown.sequence) - 1; i >= 0; i-- {
			log.Debug.Printf("%s: Running shutdown function", op)
			shutdown.sequence[i]()
		}
		shutdown.mu.Unlock()

		log.Debug.Printf("%s: Shutting down in 1 second", op)

		// Allow time for any other indirectly affected goroutines to
		// exit, since os.Exit is ruthless and ends immediately. This is
		// not strictly required, but nice when debugging and catching
		// logs on cloud.
		time.Sleep(1 * time.Second)

		// TODO: pass a reason to Shutdown and print it here and exit
		// with an error code if not cleanly.
		os.Exit(0)
	})
}

// Testing hook.
var killSleep = time.Sleep

var shutdown struct {
	mu       sync.Mutex
	c        chan os.Signal
	sequence []func()
	once     sync.Once
}

func init() {
	// Close the listener when a shutdown event happens.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-c
		Now()
	}()

	// Register the log shutdown routine here, to avoid an import cycle.
	// Shutting down the logger is the last thing we do.
	Handle(log.Flush)
}
