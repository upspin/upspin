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
)

type logEntry struct {
	ref upspin.Reference
	e   upspin.Endpoint
}

type writeBackQueue struct {
	sync.Mutex
	sc    *storeCache
	queue []logEntry // references waiting for write back.
	cond  *sync.Cond

	logLock sync.Mutex
	logf    *os.File
}

func newWriteBackQueue(sc *storeCache) (*writeBackQueue, error) {
	const op = "store/storecache.newWriteBackQueue"

	wbq := &writeBackQueue{sc: sc}
	wbq.cond = sync.NewCond(wbq)
	logName := path.Join(sc.dir, "reflog")

	// Read current log.
	var queue []logEntry
	f, err := os.Open(logName)
	if err == nil {
		b := bufio.NewReader(f)
		for {
			str, err := b.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					// Continue but log it.
					log.Info.Printf("%s: bad log line %s", op, str)
				}
				break
			}
			tokens := strings.Split(str[:len(str)-1], " ")
			switch tokens[0] {
			case "todo":
				if len(tokens) != 3 {
					log.Info.Printf("%s: bad log line %s", op, str)
					continue
				}
				e, err := upspin.ParseEndpoint(tokens[2])
				if err != nil {
					log.Info.Printf("%s: bad log line %s: %s", op, str, err)
					continue
				}
				queue = append(queue, logEntry{upspin.Reference(tokens[1]), *e})
			case "done":
				if len(tokens) != 3 {
					log.Info.Printf("%s: bad log line %s", op, str)
					continue
				}
				for i := range queue {
					if string(queue[i].ref) == tokens[1] {
						queue = append(queue[:i], queue[i+1:]...)
						break
					}
				}
			default:
				log.Info.Printf("%s: bad log line %s", op, str)
			}
		}
	}

	// Write new log.
	tmpName := logName + ".tmp"
	f, err = os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
	if err != nil {
		os.MkdirAll(sc.dir, 0700)
		f, err = os.OpenFile(tmpName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0700)
		if err != nil {
			return nil, err
		}
	}
	wbq.logf = f
	for _, e := range queue {
		if err := wbq.todo(e.ref, e.e); err != nil {
			return nil, err
		}
	}

	// Replace.
	if err := os.Rename(tmpName, logName); err != nil {
		return nil, err
	}

	// Start writers.
	for i := 0; i < writers; i++ {
		go wbq.writer(i)
	}

	return wbq, nil
}

func (wbq *writeBackQueue) writer(me int) {
	for {
		// Wait for someothing to do.
		wbq.Lock()
		for len(wbq.queue) == 0 {
			log.Debug.Printf("%d waiting", me)
			wbq.cond.Wait()
			log.Debug.Printf("%d awakened", me)
		}
		entry := wbq.queue[0]
		wbq.queue = wbq.queue[1:]
		wbq.Unlock()

		// Write it back.
		log.Debug.Printf("%d writing back %s %s", me, entry.ref, entry.e)
		if err := wbq.writeBack(entry); err != nil {
			log.Info.Printf("store/storecache.writer: writeback failed: %s", err)
			time.Sleep(250 * time.Millisecond)
			wbq.Lock()
			wbq.queue = append(wbq.queue, entry)
			wbq.Unlock()
			continue
		}

		// Success (or file has gone), append to log.
		wbq.done(entry)
	}
}

func (wbq *writeBackQueue) writeBack(entry logEntry) error {
	// Read it in.
	file := wbq.sc.cachePath(entry.ref, entry.e)
	data, err := readFromCacheFile(file)
	if err != nil {
		// Nothing we can do, log it but act like we succeeded.
		log.Info.Printf("store/storecache.writer: read for writeback failed: %s", err)
		return nil
	}

	// Try to write it back.
	store, err := bind.StoreServer(wbq.sc.cfg, entry.e)
	if err != nil {
		log.Info.Printf("store/storecache.writer: writeback failed: %s", err)
		return err
	}
	refdata, err := store.Put(data)
	if err != nil {
		log.Info.Printf("store/storecache.writer: writeback failed: %s", err)
		return err
	}
	if refdata.Reference != entry.ref {
		err := errors.E(fmt.Sprintf("refdata mismatch expected %s got %s", entry.ref, refdata.Reference))
		log.Info.Printf("store/storecache.writer: writeback failed: %s", err)
		return err
	}
	return nil
}

func (wbq *writeBackQueue) todo(ref upspin.Reference, e upspin.Endpoint) error {
	const op = "store/storecache.todo"

	// Append to log.
	wbq.logLock.Lock()
	if _, err := fmt.Fprintf(wbq.logf, "todo %s %s\n", ref, e); err != nil {
		wbq.logLock.Unlock()
		return err
	}
	if err := wbq.logf.Sync(); err != nil {
		// Scarey but not fatal.
		log.Info.Printf("%s: %s", op, err)
	}
	wbq.logLock.Unlock()

	// Queue for writers.
	wbq.Lock()
	wbq.queue = append(wbq.queue, logEntry{ref, e})
	wbq.Unlock()

	// Wake up writers.
	wbq.cond.Broadcast()
	return nil
}

func (wbq *writeBackQueue) done(entry logEntry) {
	const op = "store/storecache.done"

	// Append to log.
	wbq.logLock.Lock()
	if _, err := fmt.Fprintf(wbq.logf, "done %s %s\n", entry.ref, entry.e); err != nil {
		// Strange but not fatal.
		log.Info.Printf("%s: %s", op, err)
	}
	wbq.logLock.Unlock()
}
