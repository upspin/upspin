// Package stats implements a way of exposing statistics about a system.
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

// Stats defines a set of statistics to be tracked.
type Stats interface {
	// Update is called when an update is necessary, typically right before to a call to Print.
	Update()

	// Print writes into w a text, HTML-escaped summary of all the statistic that are tracked by this Stats.
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

// New creates a new Stats entry with the given name, which must be unique or it's rejected.
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

// OutputReport outputs to w a text, HTML-escaped report for the named stats, or for all of them if a zero-length
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
