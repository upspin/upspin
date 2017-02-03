// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storecache

import (
	"fmt"
	"os"
	"strings"
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

	// Retry interval for endpoints that we failed to Put to.
	retryInterval = 5 * time.Minute
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

	// Start scheduler.
	go wbq.scheduler()

	// Start writers.
	for i := 0; i < writers; i++ {
		go wbq.writer(i)
	}

	return wbq, nil
}

// isWriteBackFile returns false if this is not a write back file.
// It is called by the cache start up code for each file found when walking
// the cache directories.
//
// This is used to populate the write back queues on start up.
func (wbq *writeBackQueue) isWriteBackFile(path string) bool {
	const op = "store/storecache.readLog"
	f := strings.TrimSuffix(path, writeBackSuffix)
	if f == path {
		return false
	}

	// At this point we know it is a write back link so we will
	// take care of it.
	if wbq == nil {
		log.Error.Printf("%s: writeback file %s but running as write through", op, path)
		return true
	}
	f = strings.TrimPrefix(path, wbq.sc.dir+"/")
	elems := strings.Split(path, "/")
	if len(elems) != 3 {
		log.Error.Printf("%s: odd writeback file %s", op, path)
		return true
	}
	e, err := upspin.ParseEndpoint(elems[0])
	if err != nil {
		log.Error.Printf("%s: odd writeback file %s: %s", op, path, err)
		return true
	}
	wbq.request <- &request{ref: upspin.Reference(elems[2]), e: *e}
	return true
}

func (wbq *writeBackQueue) close() {
	close(wbq.die)
	for i := 0; i < writers+1; i++ {
		<-wbq.terminated
	}
}

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
	const op = "store/storecache.writer"
	for {
		// Wait for something to do.
		select {
		case r := <-wbq.ready:
			r.err = nil

			// Write it back.
			if r.err = wbq.writeBack(r); r.err != nil {
				log.Error.Printf("store/storecache.writer: writeback failed: %s", r.err)
			}
			log.Debug.Printf("%s: %s %s done", op, r.ref, r.e)
			wbq.done <- r
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

// newRequest makes a hard link to the cache file sends a request to the scheduler queue.
func (wbq *writeBackQueue) newRequest(ref upspin.Reference, e upspin.Endpoint) error {
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
