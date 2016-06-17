// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package metrics implements routines for generating and saving metrics associated with servers and clients.
package metrics

import (
	goErrors "errors"
	"fmt"
	"sync"

	"upspin.io/errors"
	"upspin.io/log"
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
	name       string
	startTime  upspin.Time
	endTime    upspin.Time
	kind       Kind    // Server, Client or Other kind of metric span.
	metric     *Metric // parent of this span; may be nil.
	parentSpan *Span   // may be nil.
}

// Saver is the common interface that all implementation-specific backends must implement
// for saving a Metric to a backend.
type Saver interface {
	// Save saves a metric to the backend.
	Save(*Metric) error
}

// Kind represents where the trace was taken: Server, Client or Other.
type Kind int

const (
	Server Kind = iota
	Client
	Other
)

// New creates a new named metric.
func New(name string) *Metric {
	return &Metric{
		name: name,
	}
}

// SetSaver sets the default Saver interface for storing Metrics onto a backend.
// It is not concurrency safe. It should be set before the first call to New.
func SetSaver(saver Saver) {
	defaultSaver = saver
}

// StarSpan starts a new span of the metric with implicit start time being the current time and Kind being Server.
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
		startTime: upspin.Now(),
		metric:    m,
		kind:      Server,
	}
	m.spans = append(m.spans, s)
	return s
}

// Done ends the metric and saves it to stable storage. Further use of the metric or any of
// its spans or subspans are invalid and may produce erroneous results or be silently dropped.
func (m *Metric) Done() error {
	// Serialize and flush to our backend.
	if defaultSaver == nil {
		return errors.E("Metric.Done", errors.Other, errNoSaver)
	}
	return defaultSaver.Save(m)
}

// End marks the end time of the span as the current time. It returns the parent metric for convenience which
// may be nil if the metric is Done.
func (s *Span) End() *Metric {
	s.endTime = upspin.Now()
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

func (s *Span) SetKind(kind Kind) {
	s.kind = kind
}

var (
	defaultSaver Saver
	errNoSaver   = goErrors.New("no default metric saver set")
)
