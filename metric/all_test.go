// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metric

import (
	"fmt"
	"testing"

	"upspin.io/errors"
)

func TestAll(t *testing.T) {
	saver := &dummySaver{
		done: make(chan bool),
	}
	RegisterSaver(saver)

	m := New("DirGet")
	m.StartSpan("getRoot").StartSpan("getInnerRoot").End()
	m.StartSpan("getCloudBytes").SetAnnotation("hello").End().Done()

	// Not much to do here other than assert we have two spans.
	if len(m.spans) != 3 {
		t.Fatalf("Expected 3 spans, got %d", len(m.spans))
	}
	expected := errors.Op("getRoot")
	if m.spans[0].Name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[0].Name)
	}
	expected = errors.Op("getInnerRoot")
	if m.spans[1].Name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[1].Name)
	}
	if m.spans[1].ParentSpan != m.spans[0] {
		t.Errorf("Expected parent span to be %q, got %v", m.spans[0].Name, m.spans[1].ParentSpan)
	}
	expected = errors.Op("getCloudBytes")
	if m.spans[2].Name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[2].Name)
	}

	// Save one more metric.
	m, sp := NewSpan("putBytes")
	sp.End()
	m.Done()

	// Finish.
	saveQueue <- nil
	<-saver.done
	close(saver.done)

	if len(saver.metricsReceived) != 2 {
		t.Fatalf("Expected 2 metrics processed, got %d", len(saver.metricsReceived))
	}
	err := verifyMetric(t, saver.metricsReceived[0], "DirGet", "getRoot", "getInnerRoot", "getCloudBytes")
	if err != nil {
		t.Fatal(err)
	}
	err = verifyMetric(t, saver.metricsReceived[1], "putBytes", "putBytes")
	if err != nil {
		t.Fatal(err)
	}

	if saver.metricsReceived[0].spans[2].Annotation != "hello" {
		t.Errorf("Expected annotation %q, got %q", expected, saver.metricsReceived[0].spans[2].Annotation)
	}
}

func TestFullChannel(t *testing.T) {
	for i := 0; i < SaveQueueLength+3; i++ {
		New("MkDir").StartSpan("putBytes").End().Done()
	}
	// If we block, this test will never finish.
}

func verifyMetric(t *testing.T, m *Metric, expectedName errors.Op, expectedSpanNames ...errors.Op) error {
	if m.Name != expectedName {
		return fmt.Errorf("Expected %q, got %q", expectedName, m.Name)
	}
	if len(m.spans) != len(expectedSpanNames) {
		return fmt.Errorf("Expected %d spans, got %d", len(expectedSpanNames), len(m.spans))
	}
	for i, s := range m.spans {
		exp := expectedSpanNames[i]
		if s.Name != exp {
			return fmt.Errorf("Expected span %d of metric %q to be named %q, got %q", i, m.Name, exp, s.Name)
		}
		if s.EndTime.IsZero() {
			// using %v because s.name may be nil.
			return fmt.Errorf("Span %d (%v) of metric %q has zero time", i, s.Name, m.Name)
		}
	}
	return nil
}

type dummySaver struct {
	done            chan bool
	metricsReceived []*Metric
}

func (d *dummySaver) Register(queue chan *Metric) {
	go func() {
		for m := range queue {
			if m == nil {
				break
			}
			d.metricsReceived = append(d.metricsReceived, m)
		}
		d.done <- true
	}()
}
