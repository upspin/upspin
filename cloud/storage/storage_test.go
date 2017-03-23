// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage_test

import (
	"reflect"
	"testing"

	"upspin.io/cloud/storage"
	"upspin.io/cloud/storage/storagetest"
	"upspin.io/errors"
)

func TestRegister(t *testing.T) {
	err := storage.Register("dummy", storagetest.DummyStorage)
	if err != nil {
		t.Fatal(err)
	}
	err = storage.Register("dummy", storagetest.DummyStorage)
	if err == nil {
		t.Fatalf("Duplicate registration should fail.")
	}
	s, err := storage.Dial("dummy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("Expected non-nil.")
	}
}

type dialingStorage struct {
	t            *testing.T
	expectedOpts storage.Opts
}

func (d *dialingStorage) new(opts *storage.Opts) (storage.Storage, error) {
	if len(opts.Opts) != len(d.expectedOpts.Opts) {
		d.t.Fatalf("Expected %d key-value pairs, got %d", len(d.expectedOpts.Opts), len(opts.Opts))
	}
	if !reflect.DeepEqual(opts.Opts, d.expectedOpts.Opts) {
		d.t.Errorf("key-value pairs mismatch. Expected %v got %v", d.expectedOpts.Opts, opts.Opts)
	}
	return nil, errors.Str("dummy error so we know this was called")
}

func TestDial(t *testing.T) {
	d := dialingStorage{t, storage.Opts{
		Opts: map[string]string{"key1": "val1", "key2": "val2", "key3": "val3"},
	}}
	err := storage.Register("dialTest", d.new)
	if err != nil {
		t.Fatal(err)
	}
	_, err = storage.Dial("dialTest",
		storage.WithKeyValue("key1", "val1"),
		storage.WithOptions("key2=val2,key3=val3"))
	if err == nil {
		t.Fatal("Expected a particular error")
	}
	if err.Error() != "dummy error so we know this was called" {
		t.Fatal(err)
	}
}
