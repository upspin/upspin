// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package log

// TODO: This test is very simple and can be improved.

import (
	"fmt"
	"testing"
)

func TestLogLevel(t *testing.T) {
	const (
		msg1  = "log line1"
		msg2  = "log line2"
		msg3  = "log line3"
		level = "info"
	)
	setFakeLogger(fmt.Sprintf("%shello: %s", msg2, msg3), false)

	SetLevel(level)
	if Level() != level {
		t.Fatalf("Expected %q, got %q", level, Level())
	}
	Debug.Println(msg1)             // not logged
	Info.Print(msg2)                // logged
	Error.Printf("hello: %s", msg3) // logged

	defaultLogger.(*fakeLogger).Verify(t)
}

func TestDisable(t *testing.T) {
	setFakeLogger("Starting server...", false)
	SetLevel("debug")
	Debug.Printf("Starting server...")
	SetLevel("disabled")
	Error.Printf("Important stuff you'll miss!")
	defaultLogger.(*fakeLogger).Verify(t)
}

func TestFatal(t *testing.T) {
	const (
		msg = "will abort anyway"
	)
	setFakeLogger(msg, true)

	SetLevel("error")
	Info.Fatal(msg)

	defaultLogger.(*fakeLogger).Verify(t)
}

func TestAt(t *testing.T) {
	SetLevel("info")

	if At("debug") {
		t.Error("Debug is expected to be disabled when level is info")
	}
	if !At("error") {
		t.Error("Error is expected to be enabled when level is info")
	}
	if !At("some random invalid level but we should log anyway for this very reason") {
		t.Error("Should log when level is invalid")
	}
}

func setFakeLogger(expected string, fatalExpected bool) {
	defaultLogger = &fakeLogger{
		expected:      expected,
		fatalExpected: fatalExpected,
	}
}

type fakeLogger struct {
	fatal         bool
	logged        string
	expected      string
	fatalExpected bool
}

func (ml *fakeLogger) Printf(format string, v ...interface{}) {
	ml.logged += fmt.Sprintf(format, v...)
}

func (ml *fakeLogger) Print(v ...interface{}) {
	ml.logged += fmt.Sprint(v...)
}

func (ml *fakeLogger) Println(v ...interface{}) {
	ml.logged += fmt.Sprintln(v...)
}

func (ml *fakeLogger) Fatal(v ...interface{}) {
	ml.fatal = true
	ml.Print(v...)
}

func (ml *fakeLogger) Fatalf(format string, v ...interface{}) {
	ml.fatal = true
	ml.Printf(format, v...)
}

func (ml *fakeLogger) Verify(t *testing.T) {
	if ml.logged != ml.expected {
		t.Errorf("Expected %q, got %q", ml.expected, ml.logged)
	}
	if ml.fatal != ml.fatalExpected {
		t.Errorf("Expected fatal %v, got %v", ml.fatalExpected, ml.fatal)
	}
}
