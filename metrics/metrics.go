// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package metrics implements routines for generating and saving metrics associated with servers and clients.
package metrics

import (
	"fmt"
	"sync"
	"time"

	"upspin.io/log"
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
	name       string
	startTime  time.Time
	endTime    time.Time
	kind       Kind    // Server, Client or Other kind of metric span.
	metric     *Metric // parent of this span; may be nil.
	parentSpan *Span   // may be nil.
}

// Saver is the common interface that all implementation-specific backends must implement
// for saving a Metric to a backend. A Saver must continously process Metrics sent over a channel, set
// during registration.
type Saver interface {
	// Register informs the Saver that new Metrics will be added to queue.
	Register(queue chan *Metric)
}

// Kind represents where the trace was taken: Server, Client or Other.
type Kind int

// Kinds of metrics.
const (
	Server Kind = iota
	Client
	Other
)

const maxChannelSize = 16

var saveQueue = make(chan *Metric, maxChannelSize)

// New creates a new named metric.
func New(name string) *Metric {
	return &Metric{
		name: name,
	}
}

// RegisterSaver registers a Saver for storing Metrics onto a backend. Any number of Savers may exist, but
// they will compete for the metrics as they are added to the channel.
func RegisterSaver(saver Saver) {
	saver.Register(saveQueue)
}

// StartSpan starts a new span of the metric with implicit start time being the current time and Kind being Server.
// Spans need not be contiguous and may or may not overlap.
func (m *Metric) StartSpan(name string) *Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Lazily allocate the spans slice.
	if m.spans == nil {
		m.spans = make([]*Span, 0, 16)
	}
	s := &Span{
		name:      fmt.Sprintf("%s.%s", m.name, name),
		startTime: time.Now(),
		metric:    m,
		kind:      Server,
	}
	m.spans = append(m.spans, s)
	return s
}

// Done ends the metric and any not-yet-ended span and saves it to stable storage. Further use of the metric or any of
// its spans or subspans are invalid and may produce erroneous results or be silently dropped.
func (m *Metric) Done() {
	// End open spans.
	m.mu.Lock() // Lock protects the slice of spans changing size.
	var zeroTime time.Time
	for _, s := range m.spans {
		if s.endTime == zeroTime {
			s.End()
		}
	}
	m.mu.Unlock()
	// Warn if channel is full.
	select {
	case saveQueue <- m:
		// Sent
	default:
		log.Error.Printf("Metric channel is full. Dropping metric %s.", m.name)
	}
}

// End marks the end time of the span as the current time. It returns the parent metric for convenience which
// may be nil if the metric is Done.
func (s *Span) End() *Metric {
	s.endTime = time.Now()
	return s.metric
}

// StartSubSpan starts a new span as a child of s with start time set to the current time.
// It may return nil if the parent Metric of s is Done.
func (s *Span) StartSubSpan(name string) *Span {
	if s.metric == nil {
		log.Error.Printf("Parent metric of span %q is nil", s.name)
		return nil
	}
	subSpan := s.metric.StartSpan(name)
	subSpan.parentSpan = s
	return subSpan
}

// Metric returns the parent metric of the span. It may be nil if the metric is Done.
func (s *Span) Metric() *Metric {
	return s.metric
}

// SetKind sets the kind of the span s.
func (s *Span) SetKind(kind Kind) {
	s.kind = kind
}
