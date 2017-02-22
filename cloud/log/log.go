// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log provides an implemention of upspin.io/log.ExternalLogger that
// sends logs to the Google Cloud Logging service.
package log // import "upspin.io/cloud/log"

import (
	"context"

	"upspin.io/log"

	"cloud.google.com/go/logging"
	"google.golang.org/api/option"
)

// Connect creates a logger that speaks to the Google Cloud Logging service for
// the given project and registers that logger with the log package.
func Connect(projectID, logName string) error {
	var err error
	client, err := logging.NewClient(context.Background(), projectID, option.WithScopes(logging.WriteScope))
	if err != nil {
		return err
	}
	log.Register(logger{
		cloud: client.Logger(logName),
	})
	return nil
}

type logger struct {
	cloud *logging.Logger
}

var severity = map[log.Level]logging.Severity{
	log.DebugLevel: logging.Debug,
	log.ErrorLevel: logging.Error,
	log.InfoLevel:  logging.Info,
}

func (l logger) Log(level log.Level, message string) {
	s, ok := severity[level]
	if !ok {
		return
	}
	l.cloud.StandardLogger(s).Print(message)
}

func (l logger) Flush() {
	l.cloud.Flush()
}
