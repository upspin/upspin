// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package log exports logging primitives that log to stderr and also to Google
// Cloud Logging.
package log // import "upspin.io/log"

import (
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

var (
	mu            sync.RWMutex
	currentLevel  = InfoLevel
	defaultLogger = newDefaultLogger(os.Stderr)
	external      ExternalLogger
)

func globals() (Level, Logger, ExternalLogger) {
	mu.RLock()
	defer mu.RUnlock()
	return currentLevel, defaultLogger, external
}

func newDefaultLogger(w io.Writer) Logger {
	return log.New(w, "", log.Ldate|log.Ltime|log.LUTC|log.Lmicroseconds)
}

// Register connects an ExternalLogger to the default logger. This may only be
// called once.
func Register(e ExternalLogger) {
	mu.Lock()
	defer mu.Unlock()

	if external != nil {
		panic("cannot register second external logger")
	}
	external = e
}

// SetOutput sets the default loggers to write to w.
// If w is nil, the default loggers are disabled.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()

	if w == nil {
		defaultLogger = nil
	} else {
		defaultLogger = newDefaultLogger(w)
	}
}

type logger struct {
	level Level
}

var _ Logger = (*logger)(nil)

// Printf writes a formatted message to the log.
func (l *logger) Printf(format string, v ...interface{}) {
	currentLevel, defaultLogger, external := globals()

	if l.level < currentLevel {
		return // Don't log at lower levels.
	}
	if external != nil {
		external.Log(l.level, fmt.Sprintf(format, v...))
	}
	if defaultLogger != nil {
		defaultLogger.Printf(format, v...)
	}
}

// Print writes a message to the log.
func (l *logger) Print(v ...interface{}) {
	currentLevel, defaultLogger, external := globals()

	if l.level < currentLevel {
		return // Don't log at lower levels.
	}
	if external != nil {
		external.Log(l.level, fmt.Sprint(v...))
	}
	if defaultLogger != nil {
		defaultLogger.Print(v...)
	}
}

// Println writes a line to the log.
func (l *logger) Println(v ...interface{}) {
	currentLevel, defaultLogger, external := globals()

	if l.level < currentLevel {
		return // Don't log at lower levels.
	}
	if external != nil {
		external.Log(l.level, fmt.Sprintln(v...))
	}
	if defaultLogger != nil {
		defaultLogger.Println(v...)
	}
}

// Fatal writes a message to the log and aborts, regardless of the current log level.
func (l *logger) Fatal(v ...interface{}) {
	_, defaultLogger, external := globals()

	if external != nil {
		external.Log(l.level, fmt.Sprint(v...))
		// Make sure we get the Fatal recorded.
		external.Flush()
		// Fall through to ensure we record it locally too.
	}
	if defaultLogger != nil {
		defaultLogger.Fatal(v...)
	} else {
		log.Fatal(v...)
	}
}

// Fatalf writes a formatted message to the log and aborts, regardless of the
// current log level.
func (l *logger) Fatalf(format string, v ...interface{}) {
	_, defaultLogger, external := globals()

	if external != nil {
		external.Log(l.level, fmt.Sprintf(format, v...))
		// Make sure we get the Fatal recorded.
		external.Flush()
		// Fall through to ensure we record it locally too.
	}
	if defaultLogger != nil {
		defaultLogger.Fatalf(format, v...)
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
	currentLevel, _, _ := globals()

	return toString(currentLevel)
}

// SetLevel sets the current level of logging.
func SetLevel(level string) error {
	l, err := toLevel(level)
	if err != nil {
		return err
	}
	mu.Lock()
	currentLevel = l
	mu.Unlock()
	return nil
}

// At returns whether the level will be logged currently.
func At(level string) bool {
	currentLevel, _, _ := globals()

	l, err := toLevel(level)
	if err != nil {
		return false
	}
	return currentLevel <= l
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
	_, _, external := globals()

	if external != nil {
		external.Flush()
	}
}
