// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"upspin.io/log"
)

// RegisterShutdown registers the onShutdown function to be run when the system
// is being shut down. The function is run at a given priority, where 0 is the
// highest priority and runs before all others. If multiple shutdown functions
// are registered at the same priority, they are run in arbitrary order.
// RegisterShutdown may be called concurrently.
func RegisterShutdown(priority uint8, onShutdown func()) {
	log.Debug.Printf("serverutil.RegisterShutdown: registering closure for priority: %d", priority)
	shutdown.mu.Lock()
	defer shutdown.mu.Unlock()

	shutdown.sequence[priority] = append(shutdown.sequence[priority], onShutdown)
}

// Shutdown calls all registered shutdown closures in their priority order and
// terminates. It only executes once and guarantees termination within a
// bounded amount of time. It's safe for many goroutines to call Shutdown.
func Shutdown() {
	const op = "serverutil.Shutdown"

	shutdown.once.Do(func() {
		log.Info.Printf("%s: Shutdown requested", op)

		shutdown.mu.Lock()

		// Ensure we terminate after a fixed amount of time.
		go func() {
			terminateSleep(1 * time.Minute)
			os.Exit(1)
		}()

		for pri, funcs := range shutdown.sequence {
			if len(funcs) == 0 {
				continue
			}
			log.Debug.Printf("%s: Running shutdown priority %d", op, pri)
			for _, f := range funcs {
				f()
			}
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

// For tests.
var terminateSleep = time.Sleep

var shutdown struct {
	mu       sync.Mutex
	c        chan os.Signal
	sequence [256][]func()
	once     sync.Once
}

func init() {
	// Close the listener when a shutdown event happens.
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM)
	go func() {
		<-c
		Shutdown()
	}()

	// Register the log shutdown routine here, to avoid an import cycle.
	// Shutting down the logger is the last thing we do.
	RegisterShutdown(255, log.Flush)
}
