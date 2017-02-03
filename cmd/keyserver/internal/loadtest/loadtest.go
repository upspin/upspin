// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"time"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/valid"

	_ "upspin.io/transports"
)

var (
	jobs             = flag.Int("jobs", 4, "number of goroutines to use")
	n                = flag.Int("n", 100, "number of requests per job")
	fakeUserMultiple = flag.Int("fake_user_multiple", 4, "number of fake users to request for each real user")
	serverAddr       = flag.String("server", "", "server endpoint to use; if empty, use the one in upspin/config file")

	errNotExist = errors.E(errors.NotExist)

	realUsers = []upspin.UserName{
		"edpin@google.com",
		"edpin+snapshot@google.com",
		"upspin-dir@upspin.io",
		"upspin-friend-test@google.com",
		"upspin-store@upspin.io",
		"upspin-test@google.com",
		// TODO: copy all from prod.
	}
)

type stats struct {
	job     int
	errors  int
	elapsed time.Duration
}

func main() {
	flag.Parse()

	done := make(chan stats)

	begin := time.Now()
	for i := 0; i < *jobs; i++ {
		go loadTest(i, done)
	}

	st := make([]stats, *jobs+1)
	for i := 0; i < *jobs; i++ {
		s := <-done
		st[s.job] = s
	}
	global := st[*jobs]
	global.elapsed = time.Now().Sub(begin)
	for i := 0; i < *jobs; i++ {
		reqsPerSec := float64(*n) / st[i].elapsed.Seconds()
		fmt.Printf("Job %d: elapsed: %f s. Errors: %d Reqs/sec: %f\n", st[i].job, st[i].elapsed.Seconds(), st[i].errors, reqsPerSec)
		global.errors = global.errors + st[i].errors
	}
	global.elapsed = global.elapsed / time.Duration(*jobs)
	fmt.Printf("Avg elapsed: %v\n", global.elapsed)
	fmt.Printf("Avg reqs/sec: %f\n", float64(*n**jobs)/global.elapsed.Seconds())
	fmt.Printf("Errors: %d\n", global.errors)
}

func loadTest(job int, done chan stats) {
	// Load test user must exist.
	cfg, err := config.InitConfig(nil)
	if *serverAddr != "" {
		e, err := upspin.ParseEndpoint(*serverAddr)
		if err != nil {
			log.Fatal("Error parsing endpoint: %s", err)
		}
		cfg = config.SetKeyEndpoint(cfg, *e)
	}

	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		log.Fatalf("Error binding to keyserver: %s", err)
	}

	begin := time.Now()
	st := stats{
		job: job,
	}

	for i := 0; i < *n; i++ {
		_, err := key.Lookup(makeRandomUserName())
		if !errors.Match(errNotExist, err) {
			st.errors++
			log.Printf("%d: Error: %s", job, err)
		}
	}
	st.elapsed = time.Now().Sub(begin)
	done <- st
}

func makeRandomUserName() upspin.UserName {
	var bytes [20]byte

	_, err := rand.Read(bytes[:])
	if err != nil {
		panic(err)
	}
	name := upspin.UserName(fmt.Sprintf("%x@%x.%x", bytes[:8], bytes[8:18], bytes[18:]))
	// This should never fail, but if we ever change rules, catch it early.
	if err = valid.UserName(name); err != nil {
		log.Fatalf("Invalid username: %s: %s", name, err)
	}
	return name
}
