// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storecache

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
)

const (
	// Number of simultaneous writers.
	writers = 20

	// Terminating characters for write back link names.
	writeBackSuffix = "_wbf"
)

type request struct {
	ref upspin.Reference
	e   upspin.Endpoint
	err error
}

type endpointQueue struct {
	queue []*request // references waiting for write back.
	live  bool
}

type writeBackQueue struct {
	sc *storeCache

	// byEndpoint contains references to be written back. This
	// is used/modifid exclusively by the scheduler goroutine.
	byEndpoint map[upspin.Endpoint]*endpointQueue

	// newRequest is used to send the scheduler new references to queue.
	request chan *request

	// ready carries requests ready for writers.
	ready chan *request

	// done carries completed requests.
	done chan *request

	// retry carries queues to retry.
	retry chan *endpointQueue

	// logf is the logfile we are appending to.
	logLock sync.Mutex
	logf    *os.File

	// Closing die signals all go routines to exit.
	die chan bool

	// Writers adn scheduler send to terminated on exit.
	terminated chan bool
}

func newWriteBackQueue(sc *storeCache) (*writeBackQueue, error) {
	const op = "store/storecache.newWriteBackQueue"

	wbq := &writeBackQueue{
		sc:         sc,
		byEndpoint: make(map[upspin.Endpoint]*endpointQueue),
		request:    make(chan *request, writers),
		ready:      make(chan *request, writers),
		done:       make(chan *request, writers),
		retry:      make(chan *endpointQueue, writers),
		die:        make(chan bool),
		terminated: make(chan bool),
	}

	go wbq.scheduler()

	// Read current log.
	logName := path.Join(sc.dir, "reflog")
	queue := wbq.readLog(logName)

	// Write new log.
	if err := os.MkdirAll(sc.dir, 0700); err != nil {
		return nil, errors.E(op, err)
	}
	tmpName := logName + ".tmp"
	var err error
	wbq.logf, err = os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		return nil, errors.E(op, err)
	}
	for _, e := range queue {
		if err := wbq.newRequest(e.ref, e.e); err != nil {
			return nil, errors.E(op, err)
		}
	}

	// Replace.
	if err := os.Rename(tmpName, logName); err != nil {
		return nil, errors.E(op, err)
	}

	// Start writers.
	for i := 0; i < writers; i++ {
		go wbq.writer(i)
	}

	return wbq, nil
}

func (wbq *writeBackQueue) readLog(logName string) []*request {
	const op = "store/storecache.readLog"

	var queue []*request
	f, err := os.Open(logName)
	if err != nil {
		return queue
	}
	defer f.Close()
	b := bufio.NewReader(f)
	for {
		str, err := b.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				// Continue but log it.
				log.Error.Printf("%s: bad log line %s", op, str)
			}
			break
		}
		tokens := strings.Split(str[:len(str)-1], " ")
		switch tokens[0] {
		case "newRequest":
			if len(tokens) != 3 {
				log.Error.Printf("%s: bad log line %s", op, str)
				continue
			}
			e, err := upspin.ParseEndpoint(tokens[2])
			if err != nil {
				log.Error.Printf("%s: bad log line %s: %s", op, str, err)
				continue
			}
			queue = append(queue, &request{ref: upspin.Reference(tokens[1]), e: *e})
		case "done":
			if len(tokens) != 3 {
				log.Error.Printf("%s: bad log line %s", op, str)
				continue
			}
			for i := range queue {
				if string(queue[i].ref) == tokens[1] && queue[i].e.String() == tokens[2] {
					queue = append(queue[:i], queue[i+1:]...)
					break
				}
			}
		default:
			log.Error.Printf("%s: bad log line %s", op, str)
		}
	}
	return queue
}

func (wbq *writeBackQueue) close() {
	close(wbq.die)
	for i := 0; i < writers+1; i++ {
		<-wbq.terminated
	}
}

const (
	retryInterval = 5 * time.Minute
)

// scheduler puts requests into the ready queue for the writers to work on.
func (wbq *writeBackQueue) scheduler() {
	for {
		select {
		case r := <-wbq.request:
			// A new request.
			epq := wbq.byEndpoint[r.e]
			if epq == nil {
				// First time you see an endpoint, assume it isn't
				// available but queue a retry to feel it out.
				epq = &endpointQueue{live: false}
				wbq.byEndpoint[r.e] = epq
				go func() { wbq.retry <- epq }()
			}
			epq.queue = append(epq.queue, r)
		case r := <-wbq.done:
			// A request has been completed.
			epq := wbq.byEndpoint[r.e]
			if r.err != nil {
				// Mark endpoint dead and retry some time later.
				epq.queue = append(epq.queue, r)
				epq.live = false
				time.AfterFunc(retryInterval, func() { wbq.retry <- epq })
			} else {
				// Mark endpoint live.
				epq.live = true
			}
		case epq := <-wbq.retry:
			// Retry the first request for an endpoint.
			if len(epq.queue) > 0 {
				r := epq.queue[0]
				select {
				case wbq.ready <- r:
					epq.queue = epq.queue[1:]
				default:
					// Queue full.
					time.AfterFunc(retryInterval, func() { wbq.retry <- epq })
					break
				}
			}
		case <-wbq.die:
			wbq.terminated <- true
			return
		}

		// Fill the ready queue.
		for {
			if !wbq.pickAndQueue() {
				break
			}
		}
	}
}

// pickAndQueue finds a request to schedule and puts it into the ready queue.
// It returns false if none found or the queue is full.
// TODO(p): I may want to make this fairer, i.e., circular queue scan.
func (wbq *writeBackQueue) pickAndQueue() bool {
	for _, q := range wbq.byEndpoint {
		if !q.live {
			continue
		}
		if len(q.queue) == 0 {
			continue
		}
		r := q.queue[0]
		select {
		case wbq.ready <- r:
			q.queue = q.queue[1:]
			return true
		default:
			// Queue full.
			break
		}
	}
	return false
}

func (wbq *writeBackQueue) writer(me int) {
	for {
		// Wait for something to do.
		select {
		case r := <-wbq.ready:
			r.err = nil

			// Write it back.
			if r.err = wbq.writeBack(r); r.err != nil {
				wbq.done <- r
				log.Error.Printf("store/storecache.writer: writeback failed: %s", r.err)
				continue
			}

			// Success (or file has gone), append to log.
			wbq.requestDone(r)
		case <-wbq.die:
			wbq.terminated <- true
			return
		}
	}
}

// writeBack returns nil on success or not transient errors.
// TODO(p): still figuring out how to tell them apart.
func (wbq *writeBackQueue) writeBack(r *request) error {
	// Read it in.
	file := wbq.sc.cachePath(r.ref, r.e) + writeBackSuffix
	data, err := readFromCacheFile(file)
	if err != nil {
		// Nothing we can do, log it but act like we succeeded.
		log.Error.Printf("store/storecache.writer: disappeared before writeback: %s", err)
		return nil
	}

	// Try to write it back.
	store, err := bind.StoreServer(wbq.sc.cfg, r.e)
	if err != nil {
		return err
	}
	refdata, err := store.Put(data)
	if err != nil {
		return err
	}
	if refdata.Reference != r.ref {
		err := errors.E(fmt.Sprintf("refdata mismatch expected %s got %s", r.ref, refdata.Reference))
		return err
	}
	if err := os.Remove(file); err != nil {
		log.Info.Printf("store/storecache.writer: fail remove after writeback: %s", err)
	}
	return nil
}

// newRequest appends a request to the log and puts it in the schedule queue.
func (wbq *writeBackQueue) newRequest(ref upspin.Reference, e upspin.Endpoint) error {
	const op = "store/storecache.newRequest"

	// Append to log and sync it.
	wbq.logLock.Lock()
	_, err := fmt.Fprintf(wbq.logf, "newRequest %s %s\n", ref, e)
	wbq.logLock.Unlock()
	if err != nil {
		return err
	}
	if err := wbq.logf.Sync(); err != nil {
		// Scary but not fatal.
		log.Error.Printf("%s: %s", op, err)
	}

	// Make a link to the cache file.
	cf := wbq.sc.cachePath(ref, e)
	wbf := cf + writeBackSuffix
	if err := os.Link(cf, wbf); err != nil {
		return err
	}

	// Let the scheduler know.
	wbq.request <- &request{ref: ref, e: e}
	return nil
}

// requestDone appends a finished request to the log and informs the scheduler.
func (wbq *writeBackQueue) requestDone(r *request) {
	const op = "store/storecache.done"

	// Append to log.
	wbq.logLock.Lock()
	if _, err := fmt.Fprintf(wbq.logf, "done %s %s\n", r.ref, r.e); err != nil {
		// Strange but not fatal.
		log.Error.Printf("%s: %s", op, err)
	}
	wbq.logLock.Unlock()

	// Let the scheduler know.
	wbq.done <- r
}
