// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"sync"
	"time"
)

const (
	// The maximum number of visitors that a Gate can track.
	rateMaxVisitors = 100000
	// The number of additional visitors (below the maximum) to remove
	// after hitting the maximum.
	rateMaxHeadroom = 100
)

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

	mu sync.Mutex // Guards the fields below.
	s  []*visitor // Ordered from least- to most-recently seen.
	m  map[string]*visitor
}

type visitor struct {
	key     string
	index   int
	seen    time.Time
	backoff time.Duration
}

// Pass attempts to pass key through the rate limiter, returning true if key is
// within the rate limit. If it returns false it also returns the duration that
// must elapse before the key will be allowed to pass again.
func (g *RateLimiter) Pass(key string) (bool, time.Duration) {
	return g.pass(time.Now(), key)
}

func (g *RateLimiter) pass(now time.Time, key string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Initialize the map lazily so that Gate
	// may be useful in its zero form.
	if g.m == nil {
		g.m = map[string]*visitor{}
	}

	reindexAll := false
	vIndex := -1

	v, ok := g.m[key]
	if !ok {
		// We haven't seen this visitor before,
		// add a map entry and permit them.
		v = &visitor{
			key:     key,
			index:   len(g.s),
			seen:    now,
			backoff: g.Backoff,
		}
		g.s = append(g.s, v)
		g.m[key] = v

		// Check whether we have exceeded the maximum number of
		// visitors that we can track at once. If so, prune back
		// to the maximum plus a bit of headroom for growth
		// (so that we don't do this every time a visitor is added).
		if len(g.s) >= rateMaxVisitors {
			// Drop least-recently-seen visitors.
			i := len(g.s) - rateMaxVisitors + rateMaxHeadroom
			for _, v := range g.s[:i] {
				delete(g.m, v.key)
			}
			g.s = g.s[i:]
			reindexAll = true
		}
	} else {
		// We have seen this visitor before.
		// If MaxBackoff has passed since their last request,
		// permit them and reset the backoff to its initial state.
		// If v.backoff has passed, permit them but double the backoff.
		// Otherwise, deny them.
		resetTime := v.seen.Add(g.Max)
		passTime := v.seen.Add(v.backoff)
		switch {
		case now.After(resetTime):
			v.backoff = g.Backoff
		case now.After(passTime):
			v.backoff *= 2
			if v.backoff > g.Max {
				v.backoff = g.Max
			}
		default:
			return false, passTime.Sub(now)
		}

		// Mark that we've seen this visitor now.
		v.seen = now
		// Move to the end of the slice.
		vIndex = v.index
		copy(g.s[vIndex:], g.s[vIndex+1:])
		g.s[len(g.s)-1] = v
	}
	// visitor.index fields are now considered invalid.

	// Find expired visitors.
	n := -1
	for j, v := range g.s {
		if !now.After(v.seen.Add(g.Max)) {
			break
		}
		delete(g.m, v.key)
		n = j
	}
	if n >= 0 {
		// Drop expired visitors.
		g.s = g.s[n+1:]
		reindexAll = true
	}

	if reindexAll {
		// Re-index all visitors.
		for j := range g.s {
			g.s[j].index = j
		}
	} else if vIndex >= 0 {
		// Re-index visitors after i.
		for j := range g.s[vIndex:] {
			g.s[j].index = j
		}
	}
	// visitor.index fields are valid once more.

	return true, 0
}
