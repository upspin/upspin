// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plain

import (
	"crypto/rand"
	"testing"

	"upspin.io/pack"
	"upspin.io/upspin"
)

var (
	context = &upspin.Context{}
	meta    = &upspin.Metadata{
		Packdata: []byte{byte(upspin.PlainPack)},
	}
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.PlainPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.PlainPack {
		t.Fatalf("expected %q got %q", plainPack{}, p)
	}
}

func TestPack(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user"
		text                 = "this is some text"
	)

	cipher, de := doPack(t, name, []byte(text))
	clear := doUnpack(t, cipher, de)

	if string(clear) != text {
		t.Errorf("text: expected %q; got %q", text, clear)
	}
}

// doPack packs the contents of data for name and returns the cipher and the dir entry.
func doPack(t testing.TB, name upspin.PathName, data []byte) ([]byte, *upspin.DirEntry) {
	packer := plainPack{}
	de := &upspin.DirEntry{
		Name: name,
	}
	n := packer.PackLen(context, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	cipher := make([]byte, n)
	m, err := packer.Pack(context, cipher, data, de)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	return cipher[:m], de
}

// doUnpack unpacks cipher for a dir entry and returns the clear text.
func doUnpack(t testing.TB, cipher []byte, de *upspin.DirEntry) []byte {
	packer := plainPack{}
	n := packer.UnpackLen(context, cipher, de)
	if n < 1 {
		t.Fatalf("UnpackLen failed with size %d", n)
	}
	clear := make([]byte, n)
	m, err := packer.Unpack(context, clear, cipher, de)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	return clear[:m]
}

func benchmarkPlainPack(b *testing.B, fileSize int) {
	data := make([]byte, fileSize)
	n, err := rand.Read(data)
	if err != nil {
		b.Fatal(err)
	}
	if n != fileSize {
		b.Fatalf("Not enough random bytes: got %d, expected %d", n, fileSize)
	}
	data = data[:n]
	for i := 0; i < b.N; i++ {
		doPack(b, upspin.PathName("bench@upspin.io/foo.txt"), data)
	}
}

func BenchmarkPlainPack_1byte(b *testing.B) {
	benchmarkPlainPack(b, 1)
}

func BenchmarkPlainPack_1kbyte(b *testing.B) {
	benchmarkPlainPack(b, 1024)
}

func BenchmarkPlainPack_1Mbyte(b *testing.B) {
	benchmarkPlainPack(b, 1024*1024)
}
