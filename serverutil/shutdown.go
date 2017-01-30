// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux windows

package serverutil

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"upspin.io/log"
)

// RegisterShutdown registers a onShutdown closure to be run when the system
// is being shutdown. The closure is run at a given priority, where 0 is the
// highest priority and runs before all others. If multiple shutdown closures
// are registered at the same priority, they are run in arbitrary order.
// RegisterShutdown may be called concurrently.
func RegisterShutdown(priority uint8, onShutdown func()) {
	shutdown.mu.Lock()
	shutdown.sequence[priority] = append(shutdown.sequence[priority], onShutdown)
	shutdown.mu.Unlock()
}

type shutdownSequence struct {
	mu           sync.Mutex
	notification chan os.Signal
	sequence     [256][]func()
}

var shutdown shutdownSequence

func init() {
	shutdown.notification = make(chan os.Signal, 1)
	signal.Notify(shutdown.notification, syscall.SIGTERM) // only works on Linux, Windows.
	go func() {
		<-shutdown.notification
		once.Do(doShutdown)
	}()
}

var once sync.Once

func doShutdown() {
	log.Debug.Printf("Shutdown requested")
	shutdown.mu.Lock()
	defer shutdown.mu.Unlock()

	// Ensure we terminate after some fixed amount of time.
	time.AfterFunc(time.Minute, func() {
		log.Error.Printf("Clean shutdown interrupted, forcing shutdown now")
		os.Exit(1)
	})

	for pri, funcs := range shutdown.sequence {
		if len(funcs) == 0 {
			continue
		}
		log.Debug.Printf("Running shutdown priority %d", pri)
		for _, f := range funcs {
			f()
		}
	}
	os.Exit(0)
}
