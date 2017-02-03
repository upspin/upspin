// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build disabled

// keyloadtester generates load on the key server and measures its throughput.
package main

// To use this load tester, it's best to ensure there is no client caching by
// commenting out the global key cache in key/remote/remote.go, in its init
// function.
//
// To test the best case scenario, where the key server is expected to have all
// users cached in memory, use the -workingset flag with a small number and
// -preauth. For example:
//
// ./keyloadtester -jobs 250 -workingset=10 -preauth
//
// This will start 250 goroutines that will generate 1000 requests for the same
// 10 random user names. Each goroutine will first establish a connection before
// starting measurements.
//
// To test more realistic scenarios, set larger working sets; larger than the
// key server's internal cache size (see key/server/server.go).
//
// To test the most adversarial scenario possible, do not set -workingset nor
// -preauth.
//

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
	"upspin.io/valid"

	_ "upspin.io/transports"
)

var (
	jobs       = flag.Int("jobs", 10, "number of goroutines to use")
	n          = flag.Int("n", 100, "number of requests per job")
	preauth    = flag.Bool("preauth", false, "whether to exclude the first request, which does more work, from the measurements")
	workingSet = flag.Int("workingset", 0, "if >0 requests the same `working_set` users over and over")
	serverAddr = flag.String("server", "", "server endpoint to use; if empty, use the one in upspin/config")
	verbose    = flag.Bool("v", false, "more verbose output")

	errNotExist = errors.E(errors.NotExist)

	userList []upspin.UserName
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

	// Ensure each test generates new test data.
	rand.Seed(int64(time.Now().Nanosecond()))

	if *workingSet > 0 {
		key := keyServer()
		userList = make([]upspin.UserName, *workingSet)
		for i := 0; i < *workingSet; i++ {
			userList[i] = randomUserName()
			key.Lookup(userList[i])
		}
	}

	comm := make(chan stats)
	readyOut := make(chan int)
	readyIn := make(chan int)

	for i := 0; i < *jobs; i++ {
		go loadTest(i, comm, readyOut, readyIn)
	}
	// Wait for all jobs to be ready.
	for i := 0; i < *jobs; i++ {
		<-readyOut
	}
	if *verbose {
		log.Printf("Starting...")
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

func keyServer() upspin.KeyServer {
	cfg, err := config.InitConfig(nil)
	if err != nil {
		log.Fatalf("A user config is required to run the load tester: %s", err)
	}
	if *serverAddr != "" {
		e, err := upspin.ParseEndpoint(*serverAddr)
		if err != nil {
			log.Fatalf("Error parsing endpoint: %s", err)
		}
		cfg = config.SetKeyEndpoint(cfg, *e)
	}

	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		log.Fatalf("Error binding to keyserver: %s", err)
	}

	return key
}

func loadTest(job int, done chan stats, readyOut, readyIn chan int) {
	key := keyServer()

	// Preauth ensures the HTTPS connection has been established and the RPC
	// auth has been done once, so there's a fresh session for this cliet on
	// the server.
	if *preauth {
		_, err := key.Lookup(randomUserName())
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
		var err error
		b := time.Now()
		if *workingSet > 0 {
			_, err = key.Lookup(userList[rand.Int31n(int32(*workingSet))])
		} else {
			_, err = key.Lookup(randomUserName())
		}
		e := time.Now().Sub(b)
		if !errors.Match(errNotExist, err) {
			st.errors++
			if *verbose {
				log.Error.Print(err)
			}
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

func randomUserName() upspin.UserName {
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
