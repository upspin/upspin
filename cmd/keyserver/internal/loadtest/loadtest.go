// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"math/rand"
	"time"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"

	_ "upspin.io/transports"
)

var (
	jobs             = flag.Int("jobs", 4, "number of goroutines to use")
	n                = flag.Int("n", 100, "number of requests per job")
	preauth          = flag.Bool("preauth", false, "whether to exclude the first request, which does auth, from the measurement")
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
	job        int
	errors     int
	elapsed    time.Duration
	maxLatency time.Duration
	minLatency time.Duration
	avgLatency time.Duration
}

func main() {
	flag.Parse()

	// Using math/rand because cyrpto/rand is too expensive as it reads
	// from /dev/random and may wait for events to happen.
	rand.Seed(int64(time.Now().Nanosecond()))

	comm := make(chan stats)
	readyOut := make(chan int)
	readyIn := make(chan int)

	for i := 0; i < *jobs; i++ {
		go loadTest(i, comm, readyOut, readyIn)
	}
	// Wait for all to be ready.
	for i := 0; i < *jobs; i++ {
		<-readyOut
	}
	begin := time.Now()
	// Set them loose.
	for i := 0; i < *jobs; i++ {
		readyIn <- 1
	}

	st := make([]stats, *jobs+1)
	for i := 0; i < *jobs; i++ {
		s := <-comm
		st[s.job] = s
	}
	global := st[*jobs]
	global.maxLatency = time.Nanosecond
	global.minLatency = time.Hour
	totalElapsed := time.Now().Sub(begin)
	for i := 0; i < *jobs; i++ {
		//reqsPerSec := float64(*n) / st[i].elapsed.Seconds()
		//fmt.Printf("Job %d: elapsed: %f s. Errors: %d Reqs/sec: %f\n", st[i].job, st[i].elapsed.Seconds(), st[i].errors, reqsPerSec)
		global.elapsed = global.elapsed + st[i].elapsed
		global.errors = global.errors + st[i].errors
		if st[i].maxLatency > global.maxLatency {
			global.maxLatency = st[i].maxLatency
		}
		if st[i].minLatency < global.minLatency {
			global.minLatency = st[i].minLatency
		}
		global.avgLatency += st[i].avgLatency
	}
	fmt.Printf("Avg reqs/sec: %f\n", float64(*n**jobs)/global.elapsed.Seconds())
	fmt.Printf("Reqs/sec: %f\n", float64(*n**jobs)/totalElapsed.Seconds())
	fmt.Printf("Max/Min latency: %v %v\n", global.maxLatency, global.minLatency)
	fmt.Printf("Avg avg latency: %v\n", global.avgLatency/time.Duration(*jobs))
	fmt.Printf("Errors: %d\n", global.errors)
	fmt.Printf("Total elapsed time: %v\n", totalElapsed)
}

func loadTest(job int, done chan stats, readyOut, readyIn chan int) {
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

	if *preauth {
		_, err = key.Lookup(makeRandomUserName())
		if !errors.Match(errNotExist, err) {
			panic(err)
		}
	}

	readyOut <- 1
	<-readyIn

	begin := time.Now()
	st := stats{
		job: job,
	}

	st.maxLatency = time.Nanosecond
	st.minLatency = time.Hour

	for i := 0; i < *n; i++ {
		b := time.Now()
		_, err := key.Lookup(makeRandomUserName())
		e := time.Now().Sub(b)
		if !errors.Match(errNotExist, err) {
			st.errors++
			log.Printf("%d: Error: %s", job, err)
		}
		if e > st.maxLatency {
			st.maxLatency = e
		}
		if e < st.minLatency {
			st.minLatency = e
		}
		st.avgLatency += e
	}
	st.elapsed = time.Now().Sub(begin)
	st.avgLatency /= time.Duration(*n)
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
	//if err = valid.UserName(name); err != nil {
	//	log.Fatalf("Invalid username: %s: %s", name, err)
	//}
	return name
}
