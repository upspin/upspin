// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"sync"
	"time"
)

// Gate implements a rate-limiting gate with exponential backoff,
// up to a specified maximum.
type Gate struct {
	Backoff time.Duration
	Max     time.Duration

	mu sync.Mutex
	s  []*visitor
	m  map[string]*visitor
}

type visitor struct {
	key     string
	index   int
	seen    time.Time
	backoff time.Duration
}

// Pass attempts to pass key through the gate,
// returning true if key is within the rate limit.
func (g *Gate) Pass(key string) bool {
	return g.pass(time.Now(), key)
}

func (g *Gate) pass(now time.Time, key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Initialize the map lazily so that Gate
	// may be useful in its zero form.
	if g.m == nil {
		g.m = map[string]*visitor{}
	}

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
		return true
	}

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
	default:
		return false
	}

	// Mark that we've seen this visitor now.
	v.seen = now
	// Move to the end of the slice.
	i := v.index
	copy(g.s[i:], g.s[i+1:])
	g.s[len(g.s)-1] = v

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
		// Re-index all.
		for j := range g.s {
			g.s[j].index = j
		}
	} else {
		// Re-index visitors after i.
		for j := range g.s[i:] {
			g.s[j].index = j
		}
	}

	return true
}
