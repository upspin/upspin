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
	// Maximum number of simultaneous writers.
	writers = 20

	// Initial number of simultaneous requests.
	initialMaxInProgress = 8

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
	queued map[upspin.Location]*request

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
		queued:       make(map[upspin.Location]*request),
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

	// maxInProgress is a highwater mark for in progress requests.
	// Whenever a timeout occurs and we have not more than maxInProgress
	// simultaneous requests, we halve maxInProgress. Whenever maxInProgress
	// requests complete while we have maxInProgress simultaneous requests
	// occuring, we increment maxInProgress. Thus we have a slowly increasing
	// but quickly decreasing limit resulting in a sawtooth that will feel
	// out how much concurrency we can use without causing the server to
	// timeout the requests.
	maxInProgress := initialMaxInProgress
	inProgress := 0
	successful := 0

	for {
		select {
		case r := <-wbq.request:
			log.Debug.Printf("%s: received %s %s", op, r.Reference, r.Endpoint)
			// Keep a map of requests so that we can handle flushes
			// and avoid Duplicates.
			if wbq.queued[r.Location] != nil {
				// Already queued. Unusual but OK.
				break
			}
			wbq.queued[r.Location] = r

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
			inp := inProgress
			inProgress--
			epq := wbq.byEndpoint[r.Endpoint]
			if r.err != nil {
				// Put request back on the queue.
				epq.queue = append(epq.queue, r)

				// If the request has timed out, it may have been because we are trying
				// too many at once. Back off if we are at or below maxInProgress. If not,
				// we are just draining after the last back off.
				if strings.Contains(r.err.Error(), "i/o timeout") {
					successful = 0
					if inp <= maxInProgress {
						maxInProgress = (maxInProgress + 1) / 2
					}
					break
				}

				// Mark endpoint dead and retry some time later.
				epq.live = false
				time.AfterFunc(retryInterval, func() { wbq.retry <- epq })
				break
			}

			// Remember that endpoint is live so we can queue more requests for it.
			epq.live = true

			// If we are at maxInProgress and succeed, remember that. After that happens
			// maxInProgress times, we increment maxInProgress.
			if inp == maxInProgress {
				successful++
			}
			if successful >= maxInProgress && maxInProgress < writers {
				successful = 0
				maxInProgress++
			}

			// Awaken everyone waiting for a flush.
			for _, c := range r.flushChans {
				log.Debug.Printf("flushing...")
				close(c)
			}
			delete(wbq.queued, r.Location)
			log.Debug.Printf("%s: %s %s done", op, r.Reference, r.Endpoint)
		case epq := <-wbq.retry:
			// Retry the first request for an endpoint.
			if len(epq.queue) > 0 {
				r := epq.queue[0]
				select {
				case wbq.ready <- r:
					inProgress++
					epq.queue = epq.queue[1:]
				default:
					// Queue full.
					time.AfterFunc(retryInterval, func() { wbq.retry <- epq })
					break
				}
			}
		case fr := <-wbq.flushRequest:
			r := wbq.queued[fr.Location]
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
	outer:
		for {
			// Round robin across the endpoints.
			for _, q := range wbq.byEndpoint {
				if inProgress >= maxInProgress {
					// Avoid timeouts.
					break outer
				}
				if !q.live {
					continue
				}
				if len(q.queue) == 0 {
					continue
				}
				r := q.queue[0]
				select {
				case wbq.ready <- r:
					inProgress++
					q.queue = q.queue[1:]
					break
				default:
					// Queue is full.
					break outer
				}
			}
		}
	}
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
