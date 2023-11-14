// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/storagetest"
	"upspin.io/errors"
	"upspin.io/upspin"
)

type noopLogger struct{}

func (noopLogger) PutAttempt(actor upspin.UserName, u *upspin.User) error {
	return nil
}
func (noopLogger) PutSuccess(actor upspin.UserName, u *upspin.User) error {
	return nil
}
func (noopLogger) ReadAll() ([]byte, error) {
	return nil, nil
}

type oneRefStorage struct {
	storage.Storage
	ref  string
	body []byte
}

func (s *oneRefStorage) Download(ref string) ([]byte, error) {
	if ref != s.ref {
		return nil, errors.E(errors.NotExist)
	}
	return append([]byte(nil), s.body...), nil
}
func (s *oneRefStorage) Put(ref string, contents []byte) error {
	if ref != s.ref {
		return errors.E(errors.Invalid)
	}
	s.body = append([]byte(nil), contents...)
	return nil
}

func TestLog(t *testing.T) {
	origLog, err := os.ReadFile("testdata/log.txt")
	if err != nil {
		t.Fatal(err)
	}

	ds, err := storagetest.DummyStorage(nil)
	if err != nil {
		t.Fatal(err)
	}
	l := &loggerImpl{
		storage: &oneRefStorage{ds, logRef, []byte{}},
	}

	for _, line := range bytes.Split(origLog, []byte("\n")) {
		if bytes.HasPrefix(line, hashPrefix) {
			continue
		}
		if len(line) == 0 {
			continue
		}

		cols := 0
		third := bytes.IndexFunc(line, func(r rune) bool {
			if r == ':' {
				cols++
				if cols == 3 {
					return true
				}
			}
			return false
		})
		now, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", string(line[:third]))
		if err != nil {
			t.Fatal(err)
		}
		line = line[third+2:]

		by := bytes.Index(line, []byte(" by "))
		kind := string(line[:by])
		line = line[by+4:]

		col := bytes.Index(line, []byte(":"))
		actor, err := strconv.Unquote(string(line[:col]))
		if err != nil {
			t.Fatal(err)
		}
		line = line[col+2:]

		var u upspin.User
		if err := json.Unmarshal(line, &u); err != nil {
			t.Logf("%q", line)
			t.Fatal(err)
		}

		if rand.Intn(10) == 0 {
			// Simulate server restart, requiring a fetch of the
			// log from storage.
			l.log = nil
		}

		if err := l.put(now, kind, upspin.UserName(actor), &u); err != nil {
			t.Fatal(err)
		}
	}

	if len(origLog) != len(l.log) {
		t.Fatalf("generated log of length %d, want %d", len(l.log), len(origLog))
	}
	s1, s2 := bufio.NewScanner(bytes.NewReader(origLog)), bufio.NewScanner(bytes.NewReader(l.log))
	var prev string
	for n := 0; ; n++ {
		s1ok := s1.Scan()
		if s1ok != s2.Scan() {
			t.Fatalf("log files out of sync at line %d", n)
		}
		if !s1ok {
			break
		}
		if s1.Text() != s2.Text() {
			t.Fatalf("inconsistency at line %d: "+
				"generated log shows:\n\t%s\n"+
				"but testdata/log.txt has:\n\t%s\n"+
				"occurred after line:\n\t%s",
				n, s2.Text(), s1.Text(), prev)
		}
		prev = s1.Text()
	}
}
