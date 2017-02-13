// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metric

import (
	"math/rand"
	"reflect"
	"testing"

	trace "google.golang.org/api/cloudtrace/v1"
)

func TestLabelsAndAnnotations(t *testing.T) {
	sink := new(sinkTraces)
	saver := newDummyGCPSaver(sink, 1, 1000, "static label", "static value")

	m := New("metric1").StartSpan("Span1").SetAnnotation("comment1").SetAnnotation("comment2").End()
	m.StartSpan("Span2").End()
	m.Done()

	newTrace := saver.prepareToSave(m)
	saver.save([]*trace.Trace{newTrace})

	// Sink should have one trace with two spans, both with the static labels and one with an annotation (the last one).
	if len(sink.traces) != 1 {
		t.Fatalf("Expected one metric, got %d", len(sink.traces))
	}
	if len(sink.traces[0].Traces) != 1 {
		t.Fatalf("Expected one trace, got %d", len(sink.traces[0].Traces))
	}
	if len(sink.traces[0].Traces[0].Spans) != 2 {
		t.Fatalf("Expected two spans, got %d", len(sink.traces[0].Traces[0].Spans))
	}
	s1 := sink.traces[0].Traces[0].Spans[0]
	s2 := sink.traces[0].Traces[0].Spans[1]
	expected := "Span1"
	if s1.Name != expected {
		t.Errorf("Expected span one to be named %q, got %q", expected, s1.Name)
	}
	expected = "Span2"
	if s2.Name != expected {
		t.Errorf("Expected span two to be named %q, got %q", expected, s2.Name)
	}
	l := map[string]string{"static label": "static value", "txt": "comment2"}
	if !reflect.DeepEqual(s1.Labels, l) {
		t.Errorf("Expected s1.Labels to match %v, got %v", l, s1.Labels)
	}
	l = map[string]string{"static label": "static value"}
	if !reflect.DeepEqual(s2.Labels, l) {
		t.Errorf("Expected s1.Labels to match %v, got %v", l, s1.Labels)
	}
}

func TestNoLabelsAndAnnotation(t *testing.T) {
	sink := new(sinkTraces)
	saver := newDummyGCPSaver(sink, 1, 1000)

	m := New("metric1").StartSpan("Span1").SetAnnotation("comment17").End()
	m.StartSpan("Span2").End()
	m.Done()

	newTrace := saver.prepareToSave(m)
	saver.save([]*trace.Trace{newTrace})

	// Sink should have one trace with two spans, one with an annotation.
	if len(sink.traces) != 1 {
		t.Fatalf("Expected one metric, got %d", len(sink.traces))
	}
	if len(sink.traces[0].Traces) != 1 {
		t.Fatalf("Expected one trace, got %d", len(sink.traces[0].Traces))
	}
	if len(sink.traces[0].Traces[0].Spans) != 2 {
		t.Fatalf("Expected two spans, got %d", len(sink.traces[0].Traces[0].Spans))
	}
	s1 := sink.traces[0].Traces[0].Spans[0]
	s2 := sink.traces[0].Traces[0].Spans[1]
	l := map[string]string{"txt": "comment17"}
	if !reflect.DeepEqual(s1.Labels, l) {
		t.Errorf("Expected s1.Labels to match %v, got %v", l, s1.Labels)
	}
	var zeroMap map[string]string
	if !reflect.DeepEqual(s2.Labels, zeroMap) {
		t.Errorf("Expected s2.Labels to match %v, got %v", zeroMap, s2.Labels)
	}
}

func TestBatchesMultipleMetrics(t *testing.T) {
	sink := new(sinkTraces)
	saver := newDummyGCPSaver(sink, 1, 1000)

	done := make(chan bool)
	onFlush = func() {
		done <- true
	}
	defer func() {
		onFlush = func() {}
	}()

	saveQueue = make(chan *Metric, 10)
	saver.Register(saveQueue)

	initialCount := NumProcessed()

	m1 := New("metric1").StartSpan("Span1").SetAnnotation("comment17").End()
	m1.Done()
	m2 := New("metric2").StartSpan("Span2").End()
	m2.Done()

	for i := 0; saver.NumProcessed() < 2 && i < 2; i++ {
		<-done
	}
	if NumProcessed() != initialCount+saver.NumProcessed() {
		t.Fatalf("Metrics saved = %d, want = %d", saver.NumProcessed(), NumProcessed())
	}
}

func TestSampling(t *testing.T) {
	sink := new(sinkTraces)
	saver := newDummyGCPSaver(sink, 10, 1000)

	saveQueue = make(chan *Metric, 100)
	registered = 0
	// We know the sequence with this random seed, so test is deterministic.
	rand.Seed(1234)
	RegisterSaver(saver)

	done := make(chan bool)
	onFlush = func() {
		done <- true
	}
	defer func() {
		onFlush = func() {}
	}()

	for i := 0; i < 100; i++ {
		m, _ := NewSpan("sampling")
		m.Done()
	}

	<-done

	// We expect about 1/10th were sampled, which is 10, but it's
	// probabilistic. 14 is what we get with the rand seed above.
	if saver.NumProcessed() != 14 {
		t.Errorf("saved = %d, want = 14 ", saver.NumProcessed())
	}
}

func newDummyGCPSaver(s traceSaver, n int, maxQPS int, labels ...string) *gcpSaver {
	saver := &gcpSaver{
		projectID:    "test",
		api:          s,
		staticLabels: makeLabels(labels),
		n:            n,
	}
	return saver
}

type sinkTraces struct {
	traces []*trace.Traces
}

func (s *sinkTraces) Save(traces *trace.Traces) error {
	s.traces = append(s.traces, traces)
	return nil
}
