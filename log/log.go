// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log exports logging primitives that log to stderr and also to Google
// Cloud Logging.
package log // import "upspin.io/log"

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
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

// Level represents the level of logging.
type Level int

// Different levels of logging.
const (
	DebugLevel Level = iota
	InfoLevel
	ErrorLevel
	DisabledLevel
)

// ExternalLogger describes a service that processes logs.
type ExternalLogger interface {
	Log(Level, string)
	Flush()
}

// The set of default loggers for each log level.
var (
	Debug = &logger{DebugLevel}
	Info  = &logger{InfoLevel}
	Error = &logger{ErrorLevel}
)

type globalState struct {
	currentLevel  Level
	defaultLogger Logger
	external      ExternalLogger
}

var (
	mu    sync.RWMutex
	state = globalState{
		currentLevel:  InfoLevel,
		defaultLogger: newDefaultLogger(os.Stderr),
	}
)

func globals() globalState {
	mu.RLock()
	defer mu.RUnlock()
	return state
}

func newDefaultLogger(w io.Writer) Logger {
	return log.New(w, "", log.Ldate|log.Ltime|log.LUTC|log.Lmicroseconds)
}

// logBridge augments the Logger type with the io.Writer interface enabling
// NewStdLogger to connect Go's standard library logger to the logger provided
// by this package.
type logBridge struct {
	Logger
}

// Write parses the standard logging line (configured with log.Lshortfile) and
// passes its message component to the logger provided by this package.
func (lb logBridge) Write(b []byte) (n int, err error) {
	var message string
	// Split "f.go:42: message" into "f.go", "42", and "message".
	parts := bytes.SplitN(b, []byte{':'}, 3)
	if len(parts) != 3 || len(parts[0]) < 1 || len(parts[2]) < 1 {
		message = fmt.Sprintf("bad log format: %s", b)
	} else {
		message = string(parts[2][1:]) // Skip leading space.
	}
	lb.Print(message)
	return len(b), nil
}

// NewStdLogger creates a *log.Logger ("log" is from the Go standard library)
// that forwards messages to the provided upspin logger using a logBridge. The
// standard logger is configured with log.Lshortfile, this log line
// format which is parsed to extract the log message (skipping the filename,
// line number) to forward it to the provided upspin logger.
func NewStdLogger(l Logger) *log.Logger {
	lb := logBridge{l}
	return log.New(lb, "", log.Lshortfile)
}

// Register connects an ExternalLogger to the default logger. This may only be
// called once.
func Register(e ExternalLogger) {
	mu.Lock()
	defer mu.Unlock()

	if state.external != nil {
		panic("cannot register second external logger")
	}
	state.external = e
}

// SetOutput sets the default loggers to write to w.
// If w is nil, the default loggers are disabled.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()

	if w == nil {
		state.defaultLogger = nil
	} else {
		state.defaultLogger = newDefaultLogger(w)
	}
}

type logger struct {
	level Level
}

var _ Logger = (*logger)(nil)

// Printf writes a formatted message to the log.
func (l *logger) Printf(format string, v ...interface{}) {
	g := globals()

	if l.level < g.currentLevel {
		return // Don't log at lower levels.
	}
	if g.external != nil {
		g.external.Log(l.level, fmt.Sprintf(format, v...))
	}
	if g.defaultLogger != nil {
		g.defaultLogger.Printf(format, v...)
	}
}

// Print writes a message to the log.
func (l *logger) Print(v ...interface{}) {
	g := globals()

	if l.level < g.currentLevel {
		return // Don't log at lower levels.
	}
	if g.external != nil {
		g.external.Log(l.level, fmt.Sprint(v...))
	}
	if g.defaultLogger != nil {
		g.defaultLogger.Print(v...)
	}
}

// Println writes a line to the log.
func (l *logger) Println(v ...interface{}) {
	g := globals()

	if l.level < g.currentLevel {
		return // Don't log at lower levels.
	}
	if g.external != nil {
		g.external.Log(l.level, fmt.Sprintln(v...))
	}
	if g.defaultLogger != nil {
		g.defaultLogger.Println(v...)
	}
}

// Fatal writes a message to the log and aborts, regardless of the current log level.
func (l *logger) Fatal(v ...interface{}) {
	g := globals()

	if g.external != nil {
		g.external.Log(l.level, fmt.Sprint(v...))
		// Make sure we get the Fatal recorded.
		g.external.Flush()
		// Fall through to ensure we record it locally too.
	}
	if g.defaultLogger != nil {
		g.defaultLogger.Fatal(v...)
	} else {
		log.Fatal(v...)
	}
}

// Fatalf writes a formatted message to the log and aborts, regardless of the
// current log level.
func (l *logger) Fatalf(format string, v ...interface{}) {
	g := globals()

	if g.external != nil {
		g.external.Log(l.level, fmt.Sprintf(format, v...))
		// Make sure we get the Fatal recorded.
		g.external.Flush()
		// Fall through to ensure we record it locally too.
	}
	if g.defaultLogger != nil {
		g.defaultLogger.Fatalf(format, v...)
	} else {
		log.Fatalf(format, v...)
	}
}

// Flush implements ExternalLogger.
func (l *logger) Flush() {
	Flush()
}

// String returns the name of the logger.
func (l *logger) String() string {
	return toString(l.level)
}

func toString(level Level) string {
	switch level {
	case InfoLevel:
		return "info"
	case DebugLevel:
		return "debug"
	case ErrorLevel:
		return "error"
	case DisabledLevel:
		return "disabled"
	}
	return "unknown"
}

func toLevel(level string) (Level, error) {
	switch level {
	case "info":
		return InfoLevel, nil
	case "debug":
		return DebugLevel, nil
	case "error":
		return ErrorLevel, nil
	case "disabled":
		return DisabledLevel, nil
	}
	return DisabledLevel, fmt.Errorf("invalid log level %q", level)
}

// GetLevel returns the current logging level.
func GetLevel() string {
	g := globals()

	return toString(g.currentLevel)
}

// SetLevel sets the current level of logging.
func SetLevel(level string) error {
	l, err := toLevel(level)
	if err != nil {
		return err
	}
	mu.Lock()
	state.currentLevel = l
	mu.Unlock()
	return nil
}

// At returns whether the level will be logged currently.
func At(level string) bool {
	g := globals()

	l, err := toLevel(level)
	if err != nil {
		return false
	}
	return g.currentLevel <= l
}

// Printf writes a formatted message to the log.
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

// Fatalf writes a formatted message to the log and aborts.
func Fatalf(format string, v ...interface{}) {
	Info.Fatalf(format, v...)
}

// Flush flushes the external logger, if any.
func Flush() {
	g := globals()

	if g.external != nil {
		g.external.Flush()
	}
}
