// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"upspin.io/upbox"
)

const upboxSchema = `
users:
  - name: ann@example.com
servers:
  - name: keyserver
  - name: storeserver
  - name: dirserver
domain: example.com
`

func TestCommands(t *testing.T) {
	schema, err := upbox.SchemaFromYAML(upboxSchema, 8000)
	if err != nil {
		t.Fatalf("setting up schema: %v", err)
	}
	err = schema.Start()
	if err != nil {
		t.Fatalf("starting schema: %v", err)
	}
	defer func() {
		err = schema.Stop()
		if err != nil {
			t.Fatalf("stopping schema: %v", err)
		}
	}()
	// Run a "user" command.
	args := []string{
		"-config=" + schema.Config(schema.Users[0].Name),
		"user",
	}
	state, cmdArgs := setup(args)
	stdout, stderr, err := run(t, state, cmdArgs...)
	if err != nil {
		t.Error(err)
	}
	if stderr != "" {
		t.Fatalf("errors running user command: %s", stderr)
	}
	if !strings.Contains(stdout, "name: ann@example.com") {
		t.Fatalf("bad result from user command:\n%s\n", stdout)
	}
}

type devNull struct{}

func (devNull) Write(b []byte) (int, error) { return len(b), nil }
func (devNull) Read([]byte) (int, error)    { return 0, io.EOF }
func (devNull) Close() error                { return nil }

func run(t *testing.T, state *State, args ...string) (stdoutStr, stderrStr string, err error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	state.SetIO(devNull{}, stdout, stderr)
	defer state.DefaultIO()
	defer func() {
		rec := recover()
		switch r := rec.(type) {
		case nil:
		case string:
			if r == "exit" {
				// OK; this was a subcommand calling exit
				return
			}
			err = fmt.Errorf("%v", r)
		case error:
			err = r
		default:
			err = fmt.Errorf("%v", r)
		}
	}()
	state.Interactive = true // So we can regain control after an error.
	state.run(args)
	return stdout.String(), stderr.String(), nil
}
