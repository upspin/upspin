// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package metric implements routines for generating and saving metrics
// associated with servers and clients.
package metric // import "upspin.io/metric"

import (
	"sync"
	"sync/atomic"
	"time"

	"upspin.io/errors"
	"upspin.io/log"
)

// Metric is a named collection of spans. A span measures time from the beginning of an event
// (for example, an RPC request) until its completion.
type Metric struct {
	Name errors.Op

	mu    sync.Mutex // protects all fields below
	spans []*Span
}

// Spans returns the Spans recorded under this Metric.
// The returned slice must not be modified.
func (m *Metric) Spans() []*Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spans
}

// A Span measures time from the beginning of an event (for example, an RPC request) until its completion.
type Span struct {
	Name       errors.Op
	StartTime  time.Time
	EndTime    time.Time
	Kind       Kind    // Server, Client or Other kind of metric span.
	Parent     *Metric // parent of this span; may be nil.
	ParentSpan *Span   // may be nil.
	Annotation string  // optional.
}

// Saver is the common interface that all implementation-specific backends must implement
// for saving a Metric to a backend. A Saver must continuously process Metrics sent over a channel, set
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

// SaveQueueLength is the size of the queue of metrics to be saved.
// Too large a queue might be wasteful and too little means metrics start to
// take time in the critical path of instrumented services.
const SaveQueueLength = 1024

// saveQueue buffers metrics to be saved to the backend.
var saveQueue = make(chan *Metric, SaveQueueLength)

// New creates a new named metric. If name is non-empty, it will prefix every
// descendant's Span name.
func New(name errors.Op) *Metric {
	return &Metric{
		Name: name,
	}
}

// NewSpan creates a new unamed metric with a newly-started named span.
func NewSpan(name errors.Op) (*Metric, *Span) {
	m := New(name)
	return m, m.StartSpan(name)
}

var (
	registered int32 // read/written atomically
)

// RegisterSaver registers a Saver for storing Metrics onto a backend.
// Only one Saver may exist or it panics.
func RegisterSaver(saver Saver) {
	if !atomic.CompareAndSwapInt32(&registered, 0, 1) {
		panic("already registered.")
	}
	saver.Register(saveQueue)
}

// StartSpan starts a new span of the metric with implicit start time being the current time and Kind being Server.
// Spans need not be contiguous and may or may not overlap.
func (m *Metric) StartSpan(name errors.Op) *Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Lazily allocate the spans slice.
	if m.spans == nil {
		m.spans = make([]*Span, 0, 16)
	}
	s := &Span{
		Name:      name,
		StartTime: time.Now(),
		Parent:    m,
		Kind:      Server,
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
		if s.EndTime == zeroTime {
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
	default:
		// Warn if channel is full.
		log.Error.Printf("metric: channel is full. Dropping metric %q.", m.Name)
	}
}

// End marks the end time of the span as the current time. It returns the parent metric for convenience which
// may be nil if the metric is Done.
func (s *Span) End() *Metric {
	s.EndTime = time.Now()
	return s.Parent
}

// StartSpan starts a new span as a child of s with start time set to the current time.
// It may return nil if the parent Metric of s is Done.
func (s *Span) StartSpan(name errors.Op) *Span {
	if s.Parent == nil {
		log.Error.Printf("metric: parent metric of span %q is nil", s.Name)
		return nil
	}
	subSpan := s.Parent.StartSpan(name)
	subSpan.ParentSpan = s
	return subSpan
}

// Metric returns the parent metric of the span. It may be nil if the metric is Done.
func (s *Span) Metric() *Metric {
	return s.Parent
}

// SetKind sets the kind of the span s and returns it.
func (s *Span) SetKind(kind Kind) *Span {
	s.Kind = kind
	return s
}

// SetAnnotation sets a custom annotation to the span s and returns it.
// If multiple annotations are set, the last one wins.
func (s *Span) SetAnnotation(annotation string) *Span {
	s.Annotation = annotation
	return s
}
