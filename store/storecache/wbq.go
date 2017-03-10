// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storecache

import (
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
	// TODO: This should be configurable.
	writers = 4

	// Terminating characters for writeback link names.
	writebackSuffix = "_wbf"

	// Retry interval for endpoints that we failed to Put to.
	retryInterval = 5 * time.Minute
)

// request represents a request to writeback a block. Each corresponds
// to a Put to the storecache.
type request struct {
	upspin.Location
	err        error       // the result of the Put() to the StoreServer.
	flushChans []chan bool // each flusher waits for its chan to close.
}

// flushRequest represents a requester waiting for the writeback to happen.
// flushed will be closed when it happens.
type flushRequest struct {
	upspin.Location
	flushed chan bool
}

// endpointQueue represents a queue of pending requests destined
// for an endpoint.
type endpointQueue struct {
	queue []*request // references waiting for writeback.
	live  bool
}

type writebackQueue struct {
	sc *storeCache

	// byEndpoint contains references to be written back. This
	// is used/modified exclusively by the scheduler goroutine.
	byEndpoint map[upspin.Endpoint]*endpointQueue

	// All queued writeback requests. Also used/modified
	// exclusively by the scheduler goroutine.
	inFlight map[upspin.Location]*request

	// request carries writeback requests to the scheduler.
	request chan *request

	// flushRequest carries flush requests to the scheduler.
	flushRequest chan *flushRequest

	// ready carries requests ready for writers.
	ready chan *request

	// done carries completed requests.
	done chan *request

	// retry carries queues to retry.
	retry chan *endpointQueue

	// Closing die signals all go routines to exit.
	die chan bool

	// Writers and scheduler send to terminated on exit.
	terminated chan bool
}

func newWritebackQueue(sc *storeCache) *writebackQueue {
	const op = "store/storecache.newWritebackQueue"

	wbq := &writebackQueue{
		sc:           sc,
		byEndpoint:   make(map[upspin.Endpoint]*endpointQueue),
		inFlight:     make(map[upspin.Location]*request),
		request:      make(chan *request, writers),
		flushRequest: make(chan *flushRequest, writers),
		ready:        make(chan *request, writers),
		done:         make(chan *request, writers),
		retry:        make(chan *endpointQueue, writers),
		die:          make(chan bool),
		terminated:   make(chan bool),
	}

	// Start scheduler.
	go wbq.scheduler()

	// Start writers.
	for i := 0; i < writers; i++ {
		go wbq.writer(i)
	}

	return wbq
}

// enqueueWritebackFile populates the writeback queue on startup.
// It returns true if this was indeed a write back file.
func (wbq *writebackQueue) enqueueWritebackFile(path string) bool {
	const op = "store/storecache.isWritebackFile"
	f := strings.TrimSuffix(path, writebackSuffix)
	if f == path {
		return false
	}

	// At this point we know it is a writeback link so we will
	// take care of it.
	if wbq == nil {
		log.Error.Printf("%s: writeback file %s but running as writethrough", op, path)
		return true
	}
	f = strings.TrimPrefix(f, wbq.sc.dir+"/")
	elems := strings.Split(f, "/")
	if len(elems) != 3 {
		log.Error.Printf("%s: odd writeback file %s", op, path)
		return true
	}
	e, err := upspin.ParseEndpoint(elems[0])
	if err != nil {
		log.Error.Printf("%s: odd writeback file %s: %s", op, path, err)
		return true
	}
	wbq.request <- &request{
		Location:   upspin.Location{Reference: upspin.Reference(elems[2]), Endpoint: *e},
		err:        nil,
		flushChans: nil,
	}
	return true
}

func (wbq *writebackQueue) close() {
	close(wbq.die)
	for i := 0; i < writers+1; i++ {
		<-wbq.terminated
	}
}

// scheduler puts requests into the ready queue for the writers to work on.
func (wbq *writebackQueue) scheduler() {
	const op = "store/storecache.scheduler"
	for {
		select {
		case r := <-wbq.request:
			log.Debug.Printf("%s: received %s %s", op, r.Reference, r.Endpoint)
			// Keep a map of requests so that we can handle flushes
			// and avoid Duplicates.
			if wbq.inFlight[r.Location] != nil {
				// Already queued. Unusual but OK.
				break
			}
			wbq.inFlight[r.Location] = r

			// A new request
			epq := wbq.byEndpoint[r.Endpoint]
			if epq == nil {
				// First time you see an endpoint, assume it isn't
				// available but queue a retry to feel it out.
				epq = &endpointQueue{live: false}
				wbq.byEndpoint[r.Endpoint] = epq
				go func() { wbq.retry <- epq }()
			}
			epq.queue = append(epq.queue, r)
		case r := <-wbq.done:
			// A request has been completed.
			epq := wbq.byEndpoint[r.Endpoint]
			if r.err != nil {
				// Mark endpoint dead and retry some time later.
				epq.queue = append(epq.queue, r)
				epq.live = false
				time.AfterFunc(retryInterval, func() { wbq.retry <- epq })
				break
			}

			// Remember that endpoint is live so we can queue more requests for it.
			epq.live = true

			// Awaken everyone waiting for a flush.
			for _, c := range r.flushChans {
				log.Debug.Printf("flushing...")
				close(c)
			}
			delete(wbq.inFlight, r.Location)
			log.Debug.Printf("%s: %s %s done", op, r.Reference, r.Endpoint)
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
		case fr := <-wbq.flushRequest:
			r := wbq.inFlight[fr.Location]
			if r == nil {
				// Not in flight
				close(fr.flushed)
				break
			}
			// Could be multiple outstanding flush requests.
			r.flushChans = append(r.flushChans, fr.flushed)
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
func (wbq *writebackQueue) pickAndQueue() bool {
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
			return false
		}
	}
	return false
}

func (wbq *writebackQueue) writer(me int) {
	for {
		// Wait for something to do.
		select {
		case r := <-wbq.ready:
			r.err = nil

			// Write it back.
			if r.err = wbq.writeback(r); r.err != nil {
				log.Error.Printf("store/storecache.writer: writeback failed: %s", r.err)
			}
			wbq.done <- r
		case <-wbq.die:
			wbq.terminated <- true
			return
		}
	}
}

// writeback returns nil on success or not transient errors.
// TODO(p): still figuring out how to tell them apart.
func (wbq *writebackQueue) writeback(r *request) error {
	// Read it in.
	file := wbq.sc.cachePath(r.Reference, r.Endpoint) + writebackSuffix
	data, err := readFromCacheFile(file)
	if err != nil {
		// Nothing we can do, log it but act like we succeeded.
		log.Error.Printf("store/storecache.writer: disappeared before writeback: %s", err)
		return nil
	}

	// Try to write it back.
	store, err := bind.StoreServer(wbq.sc.cfg, r.Endpoint)
	if err != nil {
		return err
	}
	refdata, err := store.Put(data)
	if err != nil {
		return err
	}
	if refdata.Reference != r.Reference {
		err := errors.Errorf("refdata mismatch expected %q got %q", r.Reference, refdata.Reference)
		return err
	}
	if err := os.Remove(file); err != nil {
		log.Info.Printf("store/storecache.writer: fail remove after writeback: %s", err)
	}
	return nil
}

// requestWriteback makes a hard link to the cache file sends a request to the scheduler queue.
func (wbq *writebackQueue) requestWriteback(ref upspin.Reference, e upspin.Endpoint) error {
	// Make a link to the cache file.
	cf := wbq.sc.cachePath(ref, e)
	wbf := cf + writebackSuffix
	if err := os.Link(cf, wbf); err != nil {
		if strings.Contains(err.Error(), "exists") {
			// Someone else is already writing it back.
			return nil
		}
		return err
	}

	// Let the scheduler know.
	wbq.request <- &request{upspin.Location{Reference: ref, Endpoint: e}, nil, nil}
	return nil
}

// flush waits until the indicated block has been flushed.
func (wbq *writebackQueue) flush(loc upspin.Location) {
	flushed := make(chan bool)
	wbq.flushRequest <- &flushRequest{
		Location: loc,
		flushed:  flushed,
	}
	<-flushed
}
