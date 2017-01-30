// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log exports logging primitives that log to stderr and also to Google Cloud Logging.
package log

// We call this log instead of logging for two reasons:
// 1) It's shorter to type;
// 2) it mimics Go's log package and can be used as a drop-in replacement for it.

import (
	"fmt"
	goLog "log"
	"os"

	"cloud.google.com/go/logging"
	"golang.org/x/net/context"
	"google.golang.org/api/option"
)

// Logger is the interface for logging messages.
type Logger interface {
	// Printf writes a formated message to the log.
	Printf(format string, v ...interface{})

	// Print writes a message to the log.
	Print(v ...interface{})

	// Println writes a line to the log.
	Println(v ...interface{})

	// Fatal writes a message to the log and aborts.
	Fatal(v ...interface{})

	// Fatalf writes a formated message to the log and aborts.
	Fatalf(format string, v ...interface{})
}

// level represents the level of logging.
type level int

// Different levels of logging.
const (
	debug level = iota
	info
	errors
	disabled
)

// Pre-allocated Loggers at each logging level.
var (
	Debug = newLogger(debug, logging.Debug)
	Info  = newLogger(info, logging.Info)
	Error = newLogger(errors, logging.Error)

	currentLevel  = info
	cloudLogger   *logging.Logger
	defaultLogger Logger = goLog.New(os.Stderr, "", goLog.Ldate|goLog.Ltime|goLog.LUTC|goLog.Lmicroseconds)
)

type logger struct {
	level         level
	cloudSeverity logging.Severity
}

var _ Logger = (*logger)(nil)

// Printf writes a formated message to the log.
func (l *logger) Printf(format string, v ...interface{}) {
	if l.level < currentLevel {
		return // Don't log at lower levels.
	}
	if cloudLogger != nil {
		cloudLogger.StandardLogger(l.cloudSeverity).Printf(format, v...)
	}
	defaultLogger.Printf(format, v...)
}

// Print writes a message to the log.
func (l *logger) Print(v ...interface{}) {
	if l.level < currentLevel {
		return // Don't log at lower levels.
	}
	if cloudLogger != nil {
		cloudLogger.StandardLogger(l.cloudSeverity).Print(v...)
	}
	defaultLogger.Print(v...)
}

// Println writes a line to the log.
func (l *logger) Println(v ...interface{}) {
	if l.level < currentLevel {
		return // Don't log at lower levels.
	}
	if cloudLogger != nil {
		cloudLogger.StandardLogger(l.cloudSeverity).Println(v...)
	}
	defaultLogger.Println(v...)
}

// Fatal writes a message to the log and aborts, regardless of the current log level.
func (l *logger) Fatal(v ...interface{}) {
	if cloudLogger != nil {
		cloudLogger.StandardLogger(l.cloudSeverity).Print(v...)
	}
	defaultLogger.Fatal(v...)
}

// Fatalf writes a formated message to the log and aborts, regardless of the current log level.
func (l *logger) Fatalf(format string, v ...interface{}) {
	if cloudLogger != nil {
		cloudLogger.StandardLogger(l.cloudSeverity).Printf(format, v...)
	}
	defaultLogger.Fatalf(format, v...)
}

// String returns the name of the logger.
func (l *logger) String() string {
	return toString(l.level)
}

func toString(level level) string {
	switch level {
	case info:
		return "info"
	case debug:
		return "debug"
	case errors:
		return "error"
	case disabled:
		return "disabled"
	}
	return "unknown"
}

// Level returns the current logging level.
func Level() string {
	return toString(currentLevel)
}

func toLevel(level string) (level, error) {
	switch level {
	case "info":
		return info, nil
	case "debug":
		return debug, nil
	case "error":
		return errors, nil
	case "disabled":
		return disabled, nil
	}
	return disabled, fmt.Errorf("invalid log level %q", level)
}

// SetLevel sets the current level of logging.
func SetLevel(level string) error {
	l, err := toLevel(level)
	if err != nil {
		return err
	}
	currentLevel = l
	return nil
}

// At returns whether the level will be logged currently.
func At(level string) bool {
	l, err := toLevel(level)
	if err != nil {
		return false
	}
	return currentLevel <= l
}

// Printf writes a formated message to the log.
func Printf(format string, v ...interface{}) {
	Info.Printf(format, v...)
}

// Print writes a message to the log.
func Print(v ...interface{}) {
	Info.Print(v...)
}

// Println writes a line to the log.
func Println(v ...interface{}) {
	Info.Println(v...)
}

// Fatal writes a message to the log and aborts.
func Fatal(v ...interface{}) {
	Info.Fatal(v...)
}

// Fatalf writes a formated message to the log and aborts.
func Fatalf(format string, v ...interface{}) {
	Info.Fatalf(format, v...)
}

// Connect connects all non-custom loggers (those not created by New) in this address space to a GCP Logging
// instance writing to a given logName.
func Connect(projectID, logName string) error {
	var err error
	client, err := newClient(projectID)
	if err != nil {
		return err
	}
	cloudLogger = client.Logger(logName)
	return nil
}

// newClient creates a new client connected to the GCP Logging API with an assigned logName.
func newClient(projectID string) (*logging.Client, error) {
	client, err := logging.NewClient(context.Background(), projectID, option.WithScopes(logging.WriteScope))
	if err != nil {
		return nil, err
	}
	return client, nil
}

// newLogger instantiates an implicit Logger using the default client.
func newLogger(level level, cloudSeverity logging.Severity) Logger {
	return &logger{
		level:         level,
		cloudSeverity: cloudSeverity,
	}
}

// ShutdownLogger is the shutdown routine for the logs. It exists to avoid an
// import cycle with serverutil.RegisterShutdown.
func ShutdownLogger() {
	if cloudLogger != nil {
		cloudLogger.Flush()
	}
}
