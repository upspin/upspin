// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metric

import "testing"

func TestAll(t *testing.T) {
	saver := &dummySaver{
		done: make(chan bool),
	}
	RegisterSaver(saver)

	m := New("DirGet")
	m.StartSpan("getRoot").StartSpan("getInnerRoot").End()
	m.StartSpan("getCloudBytes").End().Done()

	// Not much to do here other than assert we have two spans.
	if len(m.spans) != 3 {
		t.Fatalf("Expected 3 spans, got %d", len(m.spans))
	}
	expected := "DirGet.getRoot"
	if m.spans[0].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[0].name)
	}
	expected = "DirGet.getInnerRoot"
	if m.spans[1].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[1].name)
	}
	if m.spans[1].parentSpan != m.spans[0] {
		t.Errorf("Expected parent span to be %q, got %v", m.spans[0].name, m.spans[1].parentSpan)
	}
	expected = "DirGet.getCloudBytes"
	if m.spans[2].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[2].name)
	}

	// Save one more metric.
	New("MkDir").StartSpan("putBytes").End().Done()

	// Finish.
	saveQueue <- nil
	<-saver.done
	close(saver.done)

	if saver.count != 2 {
		t.Fatalf("Expected 2 metrics processed, got %d", saver.count)
	}
}

func TestFullChannel(t *testing.T) {
	for i := 0; i < saveQueueLength+3; i++ {
		New("MkDir").StartSpan("putBytes").End().Done()
	}
	// If we block, this test will never finish.
}

type dummySaver struct {
	// TODO: check the metrics received as well.
	count int
	done  chan bool
}

func (d *dummySaver) Register(queue chan *Metric) {
	go func() {
		for {
			select {
			case m := <-queue:
				if m == nil {
					d.done <- true
					return
				}
				d.count++
			}
		}
	}()
}
