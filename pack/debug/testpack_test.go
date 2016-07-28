// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package debugpack

import (
	"os"
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/key/inprocess"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/pack/internal"
	"upspin.io/upspin"
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
	name     upspin.PathName = userName + "/file/of/user"
	text                     = "this is some text"
	userName                 = "joe@blow.com"
)

var (
	userEndpoint  = upspin.Endpoint{Transport: upspin.InProcess}
	globalContext = context.New().SetUserName(userName).SetPacking(upspin.DebugPack).SetKeyEndpoint(userEndpoint)
)

// The values returned by PackLen and UnpackLen should be exact,
// but that is not a requirement for the Packer interface in general.
// We test the precision here though.
func TestPackLen(t *testing.T) {
	packer := testPack{}

	// First pack.
	data := []byte(text)
	de := &upspin.DirEntry{
		Name:    name,
		Packing: packer.Packing(),
	}
	n := packer.PackLen(globalContext, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	bp, err := packer.Pack(globalContext, de)
	if err != nil {
		t.Fatal("Pack:", err)
	}
	cipher, err := bp.Pack(data)
	if err != nil {
		t.Fatal("Pack:", err)
	}
	bp.SetLocation(upspin.Location{Reference: "dummy"})
	if err := bp.Close(); err != nil {
		t.Fatal("Pack:", err)
	}

	// Now unpack.
	n = packer.UnpackLen(globalContext, cipher, de)
	if n < 0 {
		t.Fatal("UnpackLen failed")
	}
	bu, err := packer.Unpack(globalContext, de)
	if err != nil {
		t.Fatal("Unpack:", err)
	}
	if _, ok := bu.NextBlock(); !ok {
		t.Fatal("NextBlock returned false")
	}
	clear, err := bu.Unpack(cipher)
	if err != nil {
		t.Fatal("Unpack:", err)
	}
	if got := string(clear); got != text {
		t.Errorf("text: got %q; want %q", got, text)
	}
}

// This one uses oversize buffers rather than what PackLen says.
// Verifies that the count returned is correct if the buffer is longer than needed.
func TestPack(t *testing.T) {
	packer := testPack{}

	// First pack.
	data := []byte(text)
	de := &upspin.DirEntry{
		Name:    name,
		Packing: packer.Packing(),
	}
	n := packer.PackLen(globalContext, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	bp, err := packer.Pack(globalContext, de)
	if err != nil {
		t.Fatal("Pack:", err)
	}
	cipher, err := bp.Pack(data)
	if err != nil {
		t.Fatal("Pack:", err)
	}
	bp.SetLocation(upspin.Location{Reference: "dummy"})
	if err := bp.Close(); err != nil {
		t.Fatal("Pack:", err)
	}

	// Now unpack.
	bu, err := packer.Unpack(globalContext, de)
	if err != nil {
		t.Fatal("Unpack:", err)
	}
	if _, ok := bu.NextBlock(); !ok {
		t.Fatal("NextBlock returned false")
	}
	clear, err := bu.Unpack(cipher)
	if err != nil {
		t.Fatal("Unpack:", err)
	}
	if got := string(clear); got != text {
		t.Errorf("text: got %q; want %q", got, text)
	}
}

func TestMain(m *testing.M) {
	user, err := bind.KeyServer(globalContext, userEndpoint)
	if err != nil {
		log.Fatalf("error binding user: %v", err)
	}
	testkey, ok := user.(*inprocess.Service)
	if !ok {
		panic("not a test KeyServer")
	}
	testkey.SetPublicKeys(userName, []upspin.PublicKey{"a key"})
	os.Exit(m.Run())
}

func TestPackdata(t *testing.T) {
	const (
		path = "foo@example.com/file"
		sig  = 42
	)
	d := &upspin.DirEntry{Name: path}

	// Construct the Packdata.
	cb, err := cryptByte(d, true)
	if err != nil {
		t.Fatal("cryptByte:", err)
	}
	if err := addSignature(d, sig); err != nil {
		t.Fatal("addSignature:", err)
	}
	putPath(d)

	// Now deconstruct it.
	if len(d.Packdata) < 3 {
		t.Fatal("bad packdata")
	}
	if got := d.Packdata[0]; got != cb {
		t.Errorf("bad crypt byte: got %v, want %v", got, cb)
	}
	if got := d.Packdata[1]; got != sig {
		t.Error("bad signature: got %v, want %v", got, sig)
	}
	p, err := getPath(d)
	if err != nil {
		t.Error("getPath:", err)
	}
	if p != path {
		t.Error("bad path: got %q, want %q", p, path)
	}
}

func TestMultiBlockRoundTrip(t *testing.T) {
	p := pack.Lookup(upspin.DebugPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	internal.TestMultiBlockRoundTrip(t, globalContext, p, userName)
}
