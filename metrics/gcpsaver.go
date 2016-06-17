package metrics

import (
	"crypto/rand"
	"fmt"
	"net/http"

	trace "google.golang.org/api/cloudtrace/v1"

	"upspin.io/upspin"
)

type gcpSaver struct {
	projectID string
	api       *trace.ProjectsService
}

var _ Saver = (*gcpSaver)(nil)

// NewGCPSaver returns a Saver that saves metrics to GCP Traces.
func NewGCPSaver(projectID string) (Saver, error) {
	srv, err := trace.New(&http.Client{})
	if err != nil {
		return nil, err
	}
	projSrv := trace.NewProjectsService(srv)
	return &gcpSaver{
		projectID: projectID,
		api:       projSrv,
	}, nil
}

// Save implements Saver. It serializes the metric in a GCP-friendly way and saves it to the
// specific GCP backend configured when the Saver was created.
func (g *gcpSaver) Save(m *Metric) error {
	traceSpans := make([]*trace.TraceSpan, len(m.spans))
	for i, s := range m.spans {
		traceSpans[i] = &trace.TraceSpan{
			SpanId:    i + 1,
			Name:      s.name,
			StartTime: formatTime(s.startTime),
			EndTime:   formatTime(s.endTime),
			Kind:      toKindString(s.kind),
		}
		if s.parentSpan != nil {
			// This can be N^2 if every span has a parent. But we should not have zillions of spans, so ok.
			r := findSpanRank(s.parentSpan, m)
			if r != -1 {
				traceSpans[i].ParentSpanId = r + 1
			}
		}
	}
	t := trace.Trace{
		ProjectId: g.projectID,
		TraceId:   makeTraceID(),
		Spans:     traceSpans,
	}
	traces := &trace.Traces{
		Traces: []*trace.Trace{t},
	}
	_, err := g.api.PatchTraces(g.projectID, traces).Do()
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

// formatTime returns a time string formated in the format expected by GCP traces: "".
func formatTime(time upspin.Time) string {
	return ""
}

// makeTraceID makes a random string containing 32 bytes of hex-encoded digits. It is what GCP Traces expects as ID.
// Because it needs to be unique within a project, we pad it with random numbers.
func makeTraceID() string {
	var b [32]byte
	n, err := rand.Read(b)
	if err != nil || n != len(b) {
		// Will probably never happen, but if it does, we just use timenow.
		ts := upspin.Now()
		id := fmt.Sprintf("%x%x%x%x%x%x%x", ts, ts, ts, ts, ts, ts, ts)
		return id[:32]
	}
	return fmt.Sprintf("%x", b)
}
