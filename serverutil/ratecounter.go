// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"fmt"
	"sync/atomic"
	"time"
	"upspin.io/errors"
)

// RateCounter is a counter that tracks how many values have been added per
// unit of time, averaged over a certain period. RateCounter is an expvar and
// thus can be used to count time-based events such as requests per second.
type RateCounter struct {
	buckets []int64 // running counts; must be used atomically.
	b       int32   // current bucket; must be used atomically.
	tick    <-chan time.Time
	d       time.Duration
}

// NewRateCounter creates a new counter that reports how many values have been
// Added per unit of time, averaged over a certain number of buckets. For
// example, to measure unit per second averaged over a minute:
// NewRateCount(60, time.Second).
func NewRateCounter(buckets int, d time.Duration) (*RateCounter, error) {
	return newRateCounter(buckets, d, time.NewTicker(d).C)
}

// onReady is called when the rate counter's loop has advanced. Used in testing.
var onReady = func() {}

func newRateCounter(buckets int, d time.Duration, tick <-chan time.Time) (*RateCounter, error) {
	if buckets < 1 {
		return nil, errors.E("serverutil.NewRatecounter", errors.Invalid, errors.Str("buckets must be > 0"))
	}
	r := &RateCounter{
		buckets: make([]int64, buckets),
		d:       d,
		tick:    tick,
	}
	go r.loop()
	return r, nil
}

// Add adds val to the counter.
func (r *RateCounter) Add(val int64) {
	bucket := atomic.LoadInt32(&r.b)
	atomic.AddInt64(&r.buckets[bucket%int32(len(r.buckets))], val)
}

// Rate returns the rate that values are Added to the counter, per unit of time,
// averaged over the number of buckets.
func (r *RateCounter) Rate() float64 {
	var sum float64
	for i := 0; i < len(r.buckets); i++ {
		sum += float64(atomic.LoadInt64(&r.buckets[i]))
	}
	return sum / float64(len(r.buckets))
}

// String implements expvar.Val
func (r *RateCounter) String() string {
	return fmt.Sprintf("%g ops/s", r.Rate()/float64(r.d.Seconds()))
}

func (r *RateCounter) loop() {
	for {
		// After each tick, move to the next bucket and zero it.
		<-r.tick
		bucket := atomic.AddInt32(&r.b, 1)
		atomic.StoreInt64(&r.buckets[bucket%int32(len(r.buckets))], 0)
		onReady()
	}
}
