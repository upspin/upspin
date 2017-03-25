// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"sync"
	"time"
)

// The maximum number of visitors that a RateLimiter can track.
const rateMaxVisitors = 100000

// RateLimiter implements a rate limiter with exponential backoff,
// up to a specified maximum.
type RateLimiter struct {
	// Backoff specifies an initial backoff duration for a key.
	// After the first request for a given key the key will be denied until
	// the backoff has passed. If another request arrives after the backoff
	// but before Max, the backoff duration is doubled.
	Backoff time.Duration

	// Max specifies a maximum backoff duration.
	Max time.Duration

	mu          sync.Mutex // Guards the fields below.
	m           map[string]*visitor
	first, last *visitor
}

type visitor struct {
	key     string
	seen    time.Time
	backoff time.Duration

	prev, next *visitor
}

// Pass attempts to pass key through the rate limiter, returning true if key is
// within the rate limit. If it returns false it also returns the duration that
// must elapse before the key will be allowed to pass again.
func (r *RateLimiter) Pass(key string) (bool, time.Duration) {
	return r.pass(time.Now(), key)
}

func (r *RateLimiter) pass(now time.Time, key string) (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Initialize the map lazily so that RateLimiter
	// may be useful in its zero form.
	if r.m == nil {
		r.m = map[string]*visitor{}
	}

	v, ok := r.m[key]
	if !ok {
		// We haven't seen this visitor before,
		// so add a map entry and permit it.
		v = &visitor{
			key:     key,
			seen:    now,
			backoff: r.Backoff,
		}
		r.m[key] = v

		// Add visitor to the end of the list.
		if r.last != nil {
			r.last.next = v
			v.prev = r.last
		}
		r.last = v

		// If the list is empty, add it at the start.
		if r.first == nil {
			r.first = v
		}
	} else {
		// We have seen this visitor before.
		// If v.backoff has passed, permit it but double the backoff.
		// Otherwise, deny it.
		// If MaxBackoff has passed since its last request,
		// permit it and reset the backoff to its initial state.
		resetTime := v.seen.Add(r.Max)
		if now.After(resetTime) {
			v.backoff = r.Backoff
		} else {
			passTime := v.seen.Add(v.backoff)

			if now.After(passTime) {
				v.backoff *= 2
				if v.backoff > r.Max {
					v.backoff = r.Max
				}
			} else {
				return false, passTime.Sub(now)
			}
		}

		// Mark that we've seen this visitor now.
		v.seen = now
		// Move v to the end of the list, if it's not there already.
		if r.last != v {
			// Remove v from the list.
			if v.prev != nil {
				v.prev.next = v.next
			} else {
				r.first = v.next
			}
			if v.next != nil {
				v.next.prev = v.prev
			}
			// Attach v to the end of the list.
			v.prev = r.last
			v.next = nil
			r.last.next = v
			r.last = v
		}
	}

	// Find and delete expired visitors.
	// Also check whether we have exceeded the maximum number of visitors
	// that we can track at once. If so, prune back to the maximum.
	drop := 0
	if len(r.m) >= rateMaxVisitors {
		drop = len(r.m) - rateMaxVisitors
	}
	for v, i := r.first, 0; v != nil; v, i = v.next, i+1 {
		if !now.After(v.seen.Add(r.Max)) && i >= drop {
			break
		}
		delete(r.m, v.key)
		r.first = v.next
		if v.next != nil {
			v.next.prev = nil
		}
	}

	return true, 0
}
