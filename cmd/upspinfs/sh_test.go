// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin

package main_test

import (
	"os"
	"os/exec"
	"testing"
)

func TestShell(t *testing.T) {
	cmd := exec.Command("./test.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err.Error())
	}
}
