// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"testing"
	"upspin.io/upspin"
)

func TestCreateNewDir(t *testing.T) {
	s := newDirServerForTesting(t, userName)

	ss := s.newSnapshotter(1)

	name := upspin.PathName(userName + "/snapshot/2016/05/09")
	entry, err := ss.makeSnapshotPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entry.Name, name; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}

	// Try to make it again.
	entry, err = ss.makeSnapshotPath(name)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entry.Name, name+".1"; got != want {
		t.Errorf("entry.Name = %q, want = %q", got, want)
	}
}
