// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package symm

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"testing"

	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/pack/internal/packtest"
	"upspin.io/test/testutil"
	"upspin.io/upspin"
)

const (
	packing = upspin.SymmPack
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.SymmPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.SymmPack {
		t.Fatalf("expected SymmPack, got %q", p)
	}
}

// packBlob packs text according to the parameters and returns the cipher.
// TODO: move to pack/internal/packtest.
func packBlob(t *testing.T, cfg upspin.Config, packer upspin.Packer, d *upspin.DirEntry, text []byte) []byte {
	d.Packing = packer.Packing()
	bp, err := packer.Pack(cfg, d)
	if err != nil {
		t.Fatal("packBlob:", d.Name, err)
	}
	cipher, err := bp.Pack(text)
	if err != nil {
		t.Fatal("packBlob:", err)
	}
	bp.SetLocation(upspin.Location{Reference: "dummy"})
	if err := bp.Close(); err != nil {
		t.Fatal("packBlob:", err)
	}
	return cipher
}

// unpackBlob unpacks cipher according to the parameters and returns the plain text.
// TODO: move to pack/internal/packtest.
func unpackBlob(t *testing.T, cfg upspin.Config, packer upspin.Packer, d *upspin.DirEntry, cipher []byte) []byte {
	bp, err := packer.Unpack(cfg, d)
	if err != nil {
		t.Fatal("unpackBlob:", err)
	}
	if _, ok := bp.NextBlock(); !ok {
		t.Fatal("unpackBlob: no next block")
	}
	text, err := bp.Unpack(cipher)
	if err != nil {
		t.Fatal("unpackBlob:", err)
	}
	return text
}

// TODO: move to pack/internal/packtest.
func testPackAndUnpack(t *testing.T, cfg upspin.Config, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Writer:     cfg.UserName(),
	}
	cipher := packBlob(t, cfg, packer, d, text)

	// Now unpack.
	clear := unpackBlob(t, cfg, packer, d, cipher)

	if !bytes.Equal(text, clear) {
		t.Errorf("text: expected %q; got %q", text, clear)
	}
}

func TestBadkeyPack(t *testing.T) {
	const (
		user upspin.UserName = "carla@upspin.io"
		name                 = upspin.PathName(user + "/file/of/carla")
	)
	cfg, packer := setup(user)
	d := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Writer:     cfg.UserName(),
	}
	d.Packing = packer.Packing()
	_, err := packer.Pack(cfg, d)
	if errors.Match(errors.E(errors.NotExist), err) {
		return // User carla has no symmsecret.upspinkey, so this err is expected.
	}
	t.Error("BadkeyPack:", err)
}

func TestPack(t *testing.T) {
	const (
		user upspin.UserName = "joe@upspin.io"
		name                 = upspin.PathName(user + "/file/of/joe")
		text                 = "this is some text"
	)
	cfg, packer := setup(user)
	testPackAndUnpack(t, cfg, packer, name, []byte(text))
}

func benchmarkPack(b *testing.B, fileSize int, unpack bool) {
	b.SetBytes(int64(fileSize))
	const user upspin.UserName = "joe@upspin.io"
	data := make([]byte, fileSize)
	n, err := rand.Read(data)
	if err != nil {
		b.Fatal(err)
	}
	if n != fileSize {
		b.Fatalf("Not enough random bytes read: %d", n)
	}
	data = data[:n]
	name := upspin.PathName(fmt.Sprintf("%s/file/of/user.%d", user, packing))
	cfg, packer := setup(user)
	for i := 0; i < b.N; i++ {
		d := &upspin.DirEntry{
			Name:       name,
			SignedName: name,
			Writer:     cfg.UserName(),
			Packing:    packer.Packing(),
		}
		bp, err := packer.Pack(cfg, d)
		if err != nil {
			b.Fatal(err)
		}
		cipher, err := bp.Pack(data)
		if err != nil {
			b.Fatal(err)
		}
		bp.SetLocation(upspin.Location{Reference: "dummy"})
		if err := bp.Close(); err != nil {
			b.Fatal(err)
		}
		if !unpack {
			continue
		}
		bu, err := packer.Unpack(cfg, d)
		if err != nil {
			b.Fatal(err)
		}
		if _, ok := bu.NextBlock(); !ok {
			b.Fatal("no next block")
		}
		clear, err := bu.Unpack(cipher)
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(clear, data) {
			b.Fatal("cleartext mismatch")
		}
	}
}

const unpack = true

func BenchmarkPack_1byte(b *testing.B)  { benchmarkPack(b, 1, !unpack) }
func BenchmarkPack_1kbyte(b *testing.B) { benchmarkPack(b, 1024, !unpack) }
func BenchmarkPack_1Mbyte(b *testing.B) { benchmarkPack(b, 1024*1024, !unpack) }

func BenchmarkPackUnpack_1byte(b *testing.B)  { benchmarkPack(b, 1, unpack) }
func BenchmarkPackUnpack_1kbyte(b *testing.B) { benchmarkPack(b, 1024, unpack) }
func BenchmarkPackUnpack_1Mbyte(b *testing.B) {
	benchmarkPack(b, 1024*1024, unpack)
}

func setup(name upspin.UserName) (upspin.Config, upspin.Packer) {
	cfg := config.SetUserName(config.New(), name)
	packer := pack.Lookup(packing)
	j := strings.IndexByte(string(name), '@')
	if j < 0 {
		log.Fatalf("malformed username %s", name)
	}
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", string(name[:j])))
	if err != nil {
		log.Fatalf("unable to initialize factotum for %s", string(name[:j]))
	}
	cfg = config.SetFactotum(cfg, f)
	return cfg, packer
}

func TestMultiBlockRoundTrip(t *testing.T) {
	const userName = upspin.UserName("aly@upspin.io")
	cfg, packer := setup(userName)
	packtest.TestMultiBlockRoundTrip(t, cfg, packer, userName)
}
