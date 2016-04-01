// Package stats implements a way for HTTP servers to expose any kind of statistics about them and their utilization.
package stats

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
)

// Stats defines what users of this package must implement.
type Stats interface {
	// Update is called when an update is necessary, typically right before to a call to Print.
	Update()

	// Print writes into w a text, HTML-escaped summary of the statistic that is being tracked.
	Print(w io.Writer)
}

var (
	// ErrExists indicates that a named Stats already exists in the global namespace.
	ErrExists = errors.New("stats already exists")

	// allStats maps a stats name to its holding struct.
	registration = make(map[string]*entry)
)

// entry holds data relevant to a given stats instance.
type entry struct {
	name       string
	lastUpdate time.Time
	stats      Stats
}

// New creates a new Stats entry with the given name (which must be unique or it's rejected) and associates a
// closure function with it. The closure function is called when the named stats is ready to be displayed, so it must
// return quickly. A nil closure is okay, as it's the user of Stats job's to keep Stats up-to-date.
func New(name string, stats Stats) error {
	_, exists := registration[name]
	if exists {
		return ErrExists
	}
	e := &entry{
		name:  name,
		stats: stats,
	}
	registration[name] = e
	return nil
}

// Lookup looks up a named Stats and if found returns it.
func Lookup(name string) Stats {
	e, found := registration[name]
	if found {
		return e.stats
	}
	return nil
}

// OutputReport outputs to w a text, HTML-escaped report for all the named stats, or for all of them if a zero-length
// slice is given.
func OutputReport(names []string, w http.ResponseWriter) error {
	n := names
	if len(n) == 0 {
		n = make([]string, len(registration))
		i := 0
		for k := range registration {
			n[i] = k
			i++
		}
	}
	var firstErr error
	for _, name := range n {
		s := Lookup(name)
		if s == nil {
			return fmt.Errorf("stats entry %s not found", name)
		}
		s.Update() // Make Stats do one final flush for its statistics.
		var b bytes.Buffer
		s.Print(&b)
		w.Header().Set(netutil.ContentType, "text/plain; charset=utf-8")
		io.WriteString(w, fmt.Sprintf("=== %s ===\n%s\n---\n", name, b.String()))
	}
	return firstErr
}
