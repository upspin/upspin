// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metric

import (
	"crypto/rand"
	"fmt"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	trace "google.golang.org/api/cloudtrace/v1"

	"upspin.io/errors"
	"upspin.io/log"
)

// traceSaver is an interface to cloudtrace API. It is used mostly for testing.
type traceSaver interface {
	// SaveTraces saves the traces to GCP.
	Save(*trace.Traces) error
}

// gcpSaver is a Saver that saves to GCP Traces.
type gcpSaver struct {
	projectID    string
	api          traceSaver
	saverQueue   chan *Metric
	staticLabels map[string]string
}

var _ Saver = (*gcpSaver)(nil)

// NewGCPSaver returns a Saver that saves metrics to GCP Traces for a GCP projectID.
// The caller must have enabled the StackDriver Traces API for the projectID and have sufficient permission
// to use the scope "cloud-platform". An optional set of string key-value pairs can be given and they
// will be saved as labels on GCP. They are useful, for example, in the case of differentiating
// a metric coming from a test instance versus production.
func NewGCPSaver(projectID string, labels ...string) (Saver, error) {
	const NewGCPSaver = "NewGCPSaver"
	// Authentication is provided by the gcloud tool when running locally, and
	// by the associated service account when running on Compute Engine.
	client, err := google.DefaultClient(context.Background(), trace.CloudPlatformScope)
	if err != nil {
		log.Fatalf("Unable to get default client: %v", err)
	}

	srv, err := trace.New(client)
	if err != nil {
		return nil, errors.E(NewGCPSaver, errors.IO, err)
	}
	if len(labels)%2 != 0 {
		return nil, errors.E(NewGCPSaver, errors.Syntax, errors.Str("metric labels must come in pairs"))
	}
	return &gcpSaver{
		projectID: projectID,
		api: &traceSaverImpl{
			projectID: projectID,
			api:       srv.Projects,
		},
		staticLabels: makeLabels(labels),
	}, nil
}

func (g *gcpSaver) Register(queue chan *Metric) {
	g.saverQueue = queue
	go g.saverLoop()
}

func (g *gcpSaver) saverLoop() {
	for {
		g.save(<-g.saverQueue)
	}
}

// save serializes the metric in a GCP-friendly way and saves it to the
// specific GCP backend configured when the Saver was created.
func (g *gcpSaver) save(m *Metric) error {
	traceSpans := make([]*trace.TraceSpan, len(m.spans))
	for i, s := range m.spans {
		var annotation map[string]string
		if s.annotation != "" {
			annotation = map[string]string{"txt": s.annotation}
		}
		traceSpans[i] = &trace.TraceSpan{
			SpanId:    uint64(i + 1),
			Name:      s.name,
			StartTime: formatTime(s.startTime),
			EndTime:   formatTime(s.endTime),
			Kind:      toKindString(s.kind),
			Labels:    mergeMaps(g.staticLabels, annotation),
		}
		if s.parentSpan != nil {
			// This can be N^2 if every span has a parent. But we should not have zillions of spans, so ok.
			r := findSpanRank(s.parentSpan, m)
			if r != -1 {
				traceSpans[i].ParentSpanId = uint64(r + 1)
			}
		}
	}
	traces := &trace.Traces{
		Traces: []*trace.Trace{
			&trace.Trace{
				ProjectId: g.projectID,
				TraceId:   makeTraceID(),
				Spans:     traceSpans,
			},
		},
	}
	err := g.api.Save(traces)
	return err
}

func toKindString(k Kind) string {
	switch k {
	case Server:
		return "RPC_SERVER"
	case Client:
		return "RPC_CLIENT"
	default:
		return "SPAN_KIND_UNSPECIFIED"
	}
}

// findSpanRank returns the position (rank) of a span within a metric.
// If not found -1 is returned.
func findSpanRank(s *Span, m *Metric) int {
	for i, span := range m.spans {
		if s == span {
			return i
		}
	}
	return -1
}

// formatTime returns a time string formatted in the format expected by GCP traces: nanoseconds from Unix epoch in the
// format "2016-06-02T14:01:23.045123456Z"
func formatTime(tm time.Time) string {
	return tm.Format(time.RFC3339Nano)
}

// makeTraceID makes a random string containing 16 bytes of hex-encoded digits. It is what GCP Traces expects as ID.
// Because it needs to be unique within a project, we pad it with random numbers.
func makeTraceID() string {
	var b [16]byte
	n, err := rand.Read(b[:])
	if err != nil || n != len(b) {
		// Will probably never happen, but if it does, we just use timenow.
		ts := time.Now()
		id := fmt.Sprintf("%x%x%x%x%x%x%x", ts, ts, ts, ts, ts, ts, ts)
		return id[:32]
	}
	return fmt.Sprintf("%x", b)
}

// makeLabels converts an even-length slice of labels to a map.
// A zero-length slice is valid and returns an empty map.
func makeLabels(labels []string) map[string]string {
	var m map[string]string
	if len(labels) > 0 {
		m = make(map[string]string, len(labels)/2+1)
	}
	for i := 0; i < len(labels); i = i + 2 {
		m[labels[i]] = labels[i+1]
	}
	return m
}

// mergeMaps merges m1 and m2 together into a new copy and returns it, without changing either m1 or m2.
func mergeMaps(m1, m2 map[string]string) map[string]string {
	var m map[string]string
	if len(m1) == 0 && len(m2) == 0 {
		return m
	}
	m = make(map[string]string, len(m1)+len(m2))
	copyMap(m, m1)
	copyMap(m, m2)
	return m
}

func copyMap(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

// traceSaverImpl is a concrete implementation of traceSaver that writes to GCP.
type traceSaverImpl struct {
	projectID string
	api       *trace.ProjectsService
}

var _ traceSaver = (*traceSaverImpl)(nil)

// Save implement SaveTraces.
func (s *traceSaverImpl) Save(traces *trace.Traces) error {
	e, err := s.api.PatchTraces(s.projectID, traces).Do()
	log.Debug.Printf("Saving Metrics to GCP: %v %v", e, err)
	return err
}
