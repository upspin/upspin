// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"os"
	"sync"
	"time"

	"upspin.io/log"
)

// RegisterShutdown registers the onShutdown closure to be run when the system
// is being shutdown. The closure is run at a given priority, where 0 is the
// highest priority and runs before all others. If multiple shutdown closures
// are registered at the same priority, they are run in arbitrary order.
// RegisterShutdown may be called concurrently.
func RegisterShutdown(priority uint8, onShutdown func()) {
	log.Printf("=== RegisterShutdown priority: %d", priority)
	shutdown.mu.Lock()
	defer shutdown.mu.Unlock()

	shutdown.sequence[priority] = append(shutdown.sequence[priority], onShutdown)
}

type shutdownSequence struct {
	mu       sync.Mutex
	c        chan os.Signal
	sequence [256][]func()
}

var (
	shutdown shutdownSequence
	once     sync.Once
)

// Shutdown calls all registered shutdown closures in their priority order and
// terminates. It only executes once and guarantees termination withtin a
// bounded amount of time.
func Shutdown() {
	once.Do(func() {
		log.Printf("==== Shutdown requested")

		shutdown.mu.Lock()
		defer shutdown.mu.Unlock()

		// Ensure we terminate after some fixed amount of time.
		time.AfterFunc(time.Minute, func() {
			log.Error.Printf("=== Clean shutdown interrupted, forcing shutdown now")
			os.Exit(1)
		})

		for pri, funcs := range shutdown.sequence {
			if len(funcs) == 0 {
				continue
			}
			log.Debug.Printf("=== Running shutdown priority %d", pri)
			for _, f := range funcs {
				f()
			}
		}
		// Allow time for any other indirectly affected goroutines to
		// exit, since os.Exit is ruthless and kills everything.
		// In particular, the remote logger
		time.Sleep(1 * time.Second)
		os.Exit(0)
	})
}
