// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build debug

package main

import (
	"log"
	"os"
	"strings"
	"time"

	"upspin.io/metric"
)

// enableMetrics turns on the metrics logging in GCP for the uspin-prod
// or upspin-test cluster.
// TODO: Generalize this.
func (s *State) enableMetrics() {
	gcloudProject := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if strings.Contains(gcloudProject, "upspin-test") {
		gcloudProject = "upspin-test"
	} else if strings.Contains(gcloudProject, "upspin-prod") {
		gcloudProject = "upspin-prod"
	} else {
		return
	}
	var err error
	if s.metricsSaver, err = metric.NewGCPSaver(gcloudProject, "app", "cmd/upspin"); err == nil {
		metric.RegisterSaver(s.metricsSaver)
	} else {
		log.Printf("saving metrics: %q", err)
	}
}

func (s *State) finishMetrics() {
	if s.metricsSaver == nil {
		return
	}
	// Allow time for metrics to propagate.
	for i := 0; metric.NumProcessed() > s.metricsSaver.NumProcessed() && i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
	}
}
