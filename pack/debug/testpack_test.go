// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package debugpack

import (
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/pack"
	"upspin.io/upspin"
	testUser "upspin.io/user/inprocess"
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.DebugPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.DebugPack {
		t.Fatalf("expected %q got %q", testPack{}, p)
	}
}

const (
	name     upspin.PathName = "user@google.com/file/of/user"
	text                     = "this is some text"
	userName                 = "joe@blow.com"
)

var (
	globalContext = context.New().SetUserName(userName)
)

// The values returned by PackLen and UnpackLen should be exact,
// but that is not a requirement for the Packer interface in general.
// We test the precision here though.
func TestPackLen(t *testing.T) {
	packer := testPack{}

	// First pack.
	data := []byte(text)
	de := &upspin.DirEntry{
		Name: name,
	}
	n := packer.PackLen(globalContext, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	cipher := make([]byte, n)
	m, err := packer.Pack(globalContext, cipher, data, de)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	if n != m {
		t.Fatalf("Pack returned %d; PackLen returned %d", m, n)
	}
	cipher = cipher[:m] // Already true, but be thorough.

	// Now unpack.
	n = packer.UnpackLen(globalContext, cipher, de)
	if n < 0 {
		t.Fatal("UnpackLen failed")
	}
	clear := make([]byte, n)
	m, err = packer.Unpack(globalContext, clear, cipher, de)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	if n != m {
		t.Fatalf("Unpack returned %d; UnpackLen returned %d", m, n)
	}
	clear = clear[:m] // Already true, but be thorough.
	str := string(clear[:m])
	if str != text {
		t.Errorf("text: expected %q; got %q", text, str)
	}
}

// This one uses oversize buffers rather than what PackLen says.
// Verifies that the count returned is correct if the buffer is longer than needed.
func TestPack(t *testing.T) {
	packer := testPack{}

	// First pack.
	data := []byte(text)
	cipher := make([]byte, 1024)
	de := &upspin.DirEntry{
		Name: name,
	}
	n := packer.PackLen(globalContext, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	m, err := packer.Pack(globalContext, cipher, data, de)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	cipher = cipher[:m]

	// Now unpack.
	clear := make([]byte, 1024)
	m, err = packer.Unpack(globalContext, clear, cipher, de)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	clear = clear[:m]
	str := string(clear[:m])
	if str != text {
		t.Errorf("text: expected %q; got %q", text, str)
	}
}

func TestMain(m *testing.M) {
	user, err := bind.User(context.New(), upspin.Endpoint{Transport: upspin.InProcess, NetAddr: ""})
	if err != nil {
		panic(err)
	}
	testuser, ok := user.(*testUser.Service)
	if !ok {
		panic("not a testuser")
	}
	testuser.SetPublicKeys(userName, []upspin.PublicKey{"a key"})
}
