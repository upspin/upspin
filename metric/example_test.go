package metric_test

import "upspin.io/metric"

func ExampleMetric() {
	// In method Lookup:
	m := metric.New("Dirserver")
	s := m.StartSpan("Lookup")
	defer m.Done()
	// do some work ...
	// ... and call getRoot, passing s to it:
	ss := s.StartSpan("getRoot")
	defer ss.End()
	// do work ...
	// return

	// Should log metric DirServer.Lookup
	// with a sub-span for getRoot covering part of the Lookup span.
}
