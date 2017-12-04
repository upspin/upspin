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
		msg1 = "log line1"
		msg2 = "log line2"
		msg3 = "log line3"
	)
	setMockLogger(fmt.Sprintf("%shello: %s", msg2, msg3), false)

	level := "info"
	SetLevel(level)
	if GetLevel() != level {
		t.Fatalf("Expected %q, got %q", level, GetLevel())
	}
	Debug.Println(msg1)             // not logged
	Info.Print(msg2)                // logged
	Error.Printf("hello: %s", msg3) // logged

	globals().defaultLogger.(*mockLogger).Verify(t)
}

func TestDisable(t *testing.T) {
	setMockLogger("Starting server...", false)
	SetLevel("debug")
	Debug.Printf("Starting server...")
	SetLevel("disabled")
	Error.Printf("Important stuff you'll miss!")
	globals().defaultLogger.(*mockLogger).Verify(t)
}

func TestFatal(t *testing.T) {
	const msg = "will abort anyway"

	setMockLogger(msg, true)

	SetLevel("error")
	Info.Fatal(msg)

	globals().defaultLogger.(*mockLogger).Verify(t)
}

func TestAt(t *testing.T) {
	SetLevel("info")

	if At("debug") {
		t.Errorf("Debug is expected to be disabled when level is info")
	}
	if !At("error") {
		t.Errorf("Error is expected to be enabled when level is info")
	}
}

func TestRemoteLogging(t *testing.T) {
	const (
		msg           = "hi, hello, how are you?"
		fatalExpected = true
	)
	mockExternal := &mockLogger{
		expected: msg,
	}
	Register(mockExternal)
	setMockLogger(msg, !fatalExpected)

	Print(msg)

	mockExternal.Verify(t)
	globals().defaultLogger.(*mockLogger).Verify(t)
}

func TestDisableLocal(t *testing.T) {
	SetOutput(nil) // disable local logging.
	Print("not printed")
}

func setMockLogger(expected string, fatalExpected bool) {
	state.defaultLogger = &mockLogger{
		expected:      expected,
		fatalExpected: fatalExpected,
	}
}

type mockLogger struct {
	fatal         bool
	logged        string
	expected      string
	fatalExpected bool
}

func (ml *mockLogger) Printf(format string, v ...interface{}) {
	ml.logged += fmt.Sprintf(format, v...)
}

func (ml *mockLogger) Print(v ...interface{}) {
	ml.logged += fmt.Sprint(v...)
}

func (ml *mockLogger) Println(v ...interface{}) {
	ml.logged += fmt.Sprintln(v...)
}

func (ml *mockLogger) Fatal(v ...interface{}) {
	ml.fatal = true
	ml.Print(v...)
}

func (ml *mockLogger) Fatalf(format string, v ...interface{}) {
	ml.fatal = true
	ml.Printf(format, v...)
}

func (ml *mockLogger) Verify(t *testing.T) {
	if ml.logged != ml.expected {
		t.Errorf("Expected %q, got %q", ml.expected, ml.logged)
	}
	if ml.fatal != ml.fatalExpected {
		t.Errorf("Expected fatal %v, got %v", ml.fatalExpected, ml.fatal)
	}
}

// mockLogger is also an ExternalLogger.
func (ml *mockLogger) Flush() {
}
func (ml *mockLogger) Log(l Level, s string) {
	ml.Print(s)
}
