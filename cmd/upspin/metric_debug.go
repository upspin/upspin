// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build debug

package main

import (
	"log"
	"os"
	"strings"

	"upspin.io/cloud/gcpsaver"
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
	if s.metricsSaver, err = gcpsaver.New(gcloudProject, "app", "cmd/upspin"); err == nil {
		metric.RegisterSaver(s.metricsSaver)
	} else {
		log.Printf("error setting up metrics: %q", err)
	}
}
