// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inprocess

import (
	"reflect"
	"testing"

	"upspin.io/upspin"

	_ "upspin.io/dir/inprocess"
	_ "upspin.io/store/inprocess"
)

var (
	inProcessEndpoint = upspin.Endpoint{
		Transport: upspin.InProcess,
	}
	testUser = upspin.User{
		Name:      "joe@blow.com",
		Dirs:      []upspin.Endpoint{inProcessEndpoint},
		Stores:    []upspin.Endpoint{inProcessEndpoint},
		PublicKey: "this is a key",
	}
)

func TestInstallAndLookup(t *testing.T) {
	key := New()
	if _, ok := key.(*server); !ok {
		t.Fatal("Not an inprocess KeyServer")
	}

	err := key.Put(&testUser)
	if err != nil {
		t.Fatal(err)
	}
	got, err := key.Lookup(testUser.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, &testUser) {
		t.Errorf("Lookup: incorrect data returned: got %v; want %v", got, &testUser)
	}
}
