// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package metrics implements routines for generating and saving metrics associated with servers and clients.
package metrics

import (
	"fmt"
	"sync"

	"upspin.io/upspin"
)

// Metric is a named collection of spans. A span measures time from the beginning of an event
// (for example, an RPC request) until its completion.
type Metric struct {
	name  string
	mu    sync.Mutex // protects all fields below
	spans []*Span
}

// A Span measures time from the beginning of an event (for example, an RPC request) until its completion.
type Span struct {
	name      string
	startTime upspin.Time
	endTime   upspin.Time
	metric    *Metric // parent of this span
}

// New creates a new named metric.
func New(name string) *Metric {
	return &Metric{
		name: name,
	}
}

// AddSpan adds a span to the metric with implicit start time being the current time.
// Spans need not be contiguous and may or may not overlap.
func (m *Metric) AddSpan(name string) *Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Lazily allocate the spans slice.
	if m.spans == nil {
		m.spans = make([]*Span, 0, 16)
	}
	s := &Span{
		name:      fmt.Sprintf("%s-%s", m.name, name),
		startTime: upspin.Now(),
		metric:    m,
	}
	m.spans = append(m.spans, s)
	return s
}

// Done ends the metric and saves it to stable storage. Further use of the metric or any of
// its spans are invalid and may produce erroneous results.
func (m *Metric) Done() {
	// Serialize and flush to our backend.
	// TBD
}

// End marks the end time of the span as the current time. It returns the parent metric for convenience which
// may be nil if the metric is Done.
func (s *Span) End() *Metric {
	s.endTime = upspin.Now()
	return s.metric
}

// Metric returns the parent metric of the span. It may be nil if the metric is Done.
func (s *Span) Metric() *Metric {
	return s.metric
}
