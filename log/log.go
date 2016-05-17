// Package log exports logging primitives that log to stderr and also to Google Cloud Logging.
package log

// We call this log instead of logging for two reasons: 1) shorter to type, 2) it mimics Go's log package and can be used
// as a drop-in replacement for it.

import (
	goLog "log"
	"os"

	"golang.org/x/net/context"
	"google.golang.org/cloud"
	"google.golang.org/cloud/logging"
)

var (
	client *logging.Client
	logErr = goLog.New(os.Stderr, "", goLog.Ldate|goLog.Ltime|goLog.LUTC|goLog.Lmicroseconds)
)

// Level is the log level, re-exported from cloud/logging.
type Level int

const (
	// Default means no assigned severity level.
	Default Level = iota
	Debug
	Info
	Warning
	Error
	Critical
	Alert
	Emergency
)

// Printf writes a formated message to the log.
func Printf(format string, v ...interface{}) {
	if client != nil {
		client.Logger(0).Printf(format, v...)
	}
	logErr.Printf(format, v...)
}

// Print writes a message to the log.
func Print(v ...interface{}) {
	if client != nil {
		client.Logger(0).Print(v...)
	}
	logErr.Print(v...)
}

// Println writes a line to the log.
func Println(v ...interface{}) {
	if client != nil {
		client.Logger(0).Println(v...)
	}
	logErr.Println(v...)
}

// Fatal writes a message to the log and aborts.
func Fatal(v ...interface{}) {
	if client != nil {
		client.Logger(0).Print(v...)
	}
	logErr.Fatal(v...)
}

// Fatalf writes a formated message to the log and aborts.
func Fatalf(format string, v ...interface{}) {
	if client != nil {
		client.Logger(0).Printf(format, v...)
	}
	logErr.Fatalf(format, v...)
}

// Logger returns a log instance for a given logging level.
func Logger(level Level) *goLog.Logger {
	if client != nil {
		return client.Logger(logging.Level(level))
	}
	return logErr
}

// Connect connects to the GCP Logging API.
func Connect(projectID, logName string) error {
	var err error
	client, err = logging.NewClient(context.Background(), projectID, logName, cloud.WithScopes(logging.Scope))
	if err != nil {
		return err
	}
	return nil
}
