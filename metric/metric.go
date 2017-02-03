// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package metric implements routines for generating and saving metrics associated with servers and clients.
package metric

import (
	"sync"
	"sync/atomic"
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
	annotation string  // optional.
}

// Saver is the common interface that all implementation-specific backends must implement
// for saving a Metric to a backend. A Saver must continuously process Metrics sent over a channel, set
// during registration.
type Saver interface {
	// Register informs the Saver that new Metrics will be added to queue.
	Register(queue chan *Metric)

	// NumProcessed returns the number of metrics processed by the saver.
	NumProcessed() int32
}

// Kind represents where the trace was taken: Server, Client or Other.
type Kind int

// Kinds of metrics.
const (
	Server Kind = iota
	Client
	Other
)

// saveQueueLength is the size of the saveQueue. Too large a queue might be wasteful and too
// little means metrics start to take time in the critical path of instrumented services.
const saveQueueLength = 1024

// saveQueue buffers metrics to be saved to the backend.
var saveQueue = make(chan *Metric, saveQueueLength)

// New creates a new named metric. If name is non-empty, it will prefix every
// descendant's Span name.
func New(name string) *Metric {
	return &Metric{
		name: name,
	}
}

// NewSpan creates a new unamed metric with a newly-started named span.
func NewSpan(name string) (*Metric, *Span) {
	m := New(name)
	return m, m.StartSpan(name)
}

var (
	registered int32 // read/written atomically
	processed  int32
)

// RegisterSaver registers a Saver for storing Metrics onto a backend. Only one Saver may exist or it panics.
// Hence, RegisterSaver is not concurrency-safe.
func RegisterSaver(saver Saver) {
	if !atomic.CompareAndSwapInt32(&registered, 0, 1) {
		panic("already registered.")
	}
	saver.Register(saveQueue)
}

// StartSpan starts a new span of the metric with implicit start time being the current time and Kind being Server.
// Spans need not be contiguous and may or may not overlap.
func (m *Metric) StartSpan(name string) *Span {
	return &Span{}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Lazily allocate the spans slice.
	if m.spans == nil {
		m.spans = make([]*Span, 0, 16)
	}
	s := &Span{
		name:      name,
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
	return
	// End open spans.
	m.mu.Lock() // Lock protects the slice of spans changing size.
	var zeroTime time.Time
	for _, s := range m.spans {
		if s.endTime == zeroTime {
			s.End()
		}
	}
	m.mu.Unlock()

	if atomic.LoadInt32(&registered) == 0 {
		// No saver registered,
		// don't send any metrics.
		return
	}

	select {
	case saveQueue <- m:
		// Sent
		atomic.AddInt32(&processed, 1)
	default:
		// Warn if channel is full.
		log.Error.Printf("metric: channel is full. Dropping metric %q.", m.name)
	}
}

// End marks the end time of the span as the current time. It returns the parent metric for convenience which
// may be nil if the metric is Done.
func (s *Span) End() *Metric {
	return nil
	s.endTime = time.Now()
	return s.metric
}

// StartSpan starts a new span as a child of s with start time set to the current time.
// It may return nil if the parent Metric of s is Done.
func (s *Span) StartSpan(name string) *Span {
	return &Span{}
	if s.metric == nil {
		log.Error.Printf("metric: parent metric of span %q is nil", s.name)
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

// SetKind sets the kind of the span s and returns it.
func (s *Span) SetKind(kind Kind) *Span {
	s.kind = kind
	return s
}

// SetAnnotation sets a custom annotation to the span s and returns it.
// If multiple annotations are set, the last one wins.
func (s *Span) SetAnnotation(annotation string) *Span {
	s.annotation = annotation
	return s
}

// NumProcessed returns the number of metrics sent to the saver for storage.
func NumProcessed() int32 {
	return atomic.LoadInt32(&processed)
}
