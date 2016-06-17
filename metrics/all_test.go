// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metrics

import "testing"

func TestAll(t *testing.T) {
	m := New("StoreServer")
	m.StartSpan("Auth").End()
	m.StartSpan("Get").End().Done()

	// Not much to do here other than assert we have two spans.
	if len(m.spans) != 2 {
		t.Fatalf("Expected 2 spans, got %d", len(m.spans))
	}
	expected := "StoreServer.Auth"
	if m.spans[0].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[0].name)
	}
	expected = "StoreServer.Get"
	if m.spans[1].name != expected {
		t.Errorf("Expected span named %q, got %q", expected, m.spans[1].name)
	}
}
