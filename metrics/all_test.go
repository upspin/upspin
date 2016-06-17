// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metrics

import (
	"testing"
	"time"
)

func TestAll(t *testing.T) {
	saver := &dummySaver{}
	RegisterSaver(saver)

	m := New("DirGet")
	m.StartSpan("getRoot").End()
	m.StartSpan("getCloudBytes").End().Done()

	// Not much to do here other than assert we have two spans.
	if len(m.spans) != 2 {
		t.Fatalf("Expected 2 spans, got %d", len(m.spans))
	}
	expected := "DirGet.getRoot"
	if m.spans[0].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[0].name)
	}
	expected = "DirGet.getCloudBytes"
	if m.spans[1].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[1].name)
	}

	// Save one more metric.
	New("MkDir").StartSpan("putBytes").End().Done()

	// Finish.
	saveQueue <- nil
	time.Sleep(10 * time.Millisecond)

	if saver.count != 2 {
		t.Fatalf("Expected 2 metrics processed, got %d", saver.count)
	}
}

func TestFullChannel(t *testing.T) {
	for i := 0; i < maxChannelSize+3; i++ {
		New("MkDir").StartSpan("putBytes").End().Done()
	}
	// If we block, this test will never finish.
}

type dummySaver struct {
	count int
}

func (d *dummySaver) Register(queue chan *Metric) {
	go func() {
		for {
			m := <-queue
			if m == nil {
				return
			}
			d.count++
		}
	}()
}
