package metrics

import (
	"crypto/rand"
	"fmt"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	trace "google.golang.org/api/cloudtrace/v1"

	"upspin.io/log"
)

// gcpSaver is a Saver that saves to GCP Traces.
type gcpSaver struct {
	projectID  string
	api        *trace.ProjectsService
	saverQueue chan *Metric
}

var _ Saver = (*gcpSaver)(nil)

// NewGCPSaver returns a Saver that saves metrics to GCP Traces for a GCP projectID.
// The caller must have enabled the StackDriver Traces API for the projectID and have sufficient permission
// to use the scope "cloud-platform". See
func NewGCPSaver(projectID string) (Saver, error) {
	// Authentication is provided by the gcloud tool when running locally, and
	// by the associated service account when running on Compute Engine.
	client, err := google.DefaultClient(context.Background(), trace.CloudPlatformScope)
	if err != nil {
		log.Fatalf("Unable to get default client: %v", err)
	}

	srv, err := trace.New(client)
	if err != nil {
		return nil, err
	}
	return &gcpSaver{
		projectID: projectID,
		api:       srv.Projects,
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
		traceSpans[i] = &trace.TraceSpan{
			SpanId:    uint64(i + 1),
			Name:      s.name,
			StartTime: formatTime(s.startTime),
			EndTime:   formatTime(s.endTime),
			Kind:      toKindString(s.kind),
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
	e, err := g.api.PatchTraces(g.projectID, traces).Do()
	log.Debug.Printf("Saving Metrics to GCP: %v %v", e, err)
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
