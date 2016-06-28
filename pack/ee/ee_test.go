// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/test/testfixtures"
	"upspin.io/upspin"
)

const (
	packing = upspin.EEPack
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.EEPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.EEPack {
		t.Fatalf("expected EEPack, got %q", p)
	}
}

// packBlob packs text according to the parameters and returns the cipher.
func packBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, d *upspin.DirEntry, text []byte) []byte {
	cipher := make([]byte, packer.PackLen(ctx, text, d))
	m, err := packer.Pack(ctx, cipher, text, d)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	return cipher[:m]
}

// unpackBlob unpacks cipher according to the parameters and returns the plain text.
func unpackBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, d *upspin.DirEntry, cipher []byte) []byte {
	clear := make([]byte, packer.UnpackLen(ctx, cipher, d))
	m, err := packer.Unpack(ctx, clear, cipher, d)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	return clear[:m]
}

// shareBlob updates the packdata of a blob such that the public keys given are readers of the blob.
func shareBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, readers []upspin.PublicKey, packdata *[]byte) {
	pd := make([]*[]byte, 1)
	pd[0] = packdata
	packer.Share(ctx, readers, pd)
}

func testPackAndUnpack(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{}
	d.Name = name
	d.Metadata.Writer = ctx.UserName
	cipher := packBlob(t, ctx, packer, d, text)

	// Now unpack.
	clear := unpackBlob(t, ctx, packer, d, cipher)

	if !bytes.Equal(text, clear) {
		t.Errorf("text: expected %q; got %q", text, clear)
	}
}

func testPackNameAndUnpack(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name, newName upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{}
	d.Name = name
	d.Metadata.Writer = ctx.UserName
	cipher := packBlob(t, ctx, packer, d, text)

	// Name to newName.
	if err := packer.Name(ctx, d, newName); err != nil {
		t.Errorf("Name failed: %s", err)
	}
	if d.Name != newName {
		t.Errorf("Name failed to set the name")
	}

	// Now unpack.
	clear := unpackBlob(t, ctx, packer, d, cipher)

	if !bytes.Equal(text, clear) {
		t.Errorf("text: expected %q; got %q", text, clear)
	}
}

func TestPack256(t *testing.T) {
	const (
		user upspin.UserName = "user@google.com"
		name                 = upspin.PathName(user + "/file/of/user.256")
		text                 = "this is some text 256"
	)
	ctx, packer := setup(user, "p256")
	testPackAndUnpack(t, ctx, packer, name, []byte(text))
}

func TestName256(t *testing.T) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.256")
		newName                 = upspin.PathName(user + "/file/of/user.256.2")
		text                    = "this is some text 256"
	)
	ctx, packer := setup(user, "p256")
	testPackNameAndUnpack(t, ctx, packer, name, newName, []byte(text))
}

func benchmarkPack(b *testing.B, curveName string, fileSize int, unpack bool) {
	const user upspin.UserName = "user@google.com"
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
	ctx, packer := setup(user, curveName)
	for i := 0; i < b.N; i++ {
		d := &upspin.DirEntry{
			Name: name,
		}
		cipher := make([]byte, packer.PackLen(ctx, data, d))
		m, err := packer.Pack(ctx, cipher, data, d)
		if err != nil {
			b.Fatal(err)
		}
		if !unpack {
			continue
		}
		cipher = cipher[:m]
		clear := make([]byte, packer.UnpackLen(ctx, cipher, d))
		m, _ = packer.Unpack(ctx, clear, cipher, d)
		clear = clear[:m]
	}
}

const unpack = true

func BenchmarkPack256_1byte(b *testing.B)  { benchmarkPack(b, "p256", 1, !unpack) }
func BenchmarkPack256_1kbyte(b *testing.B) { benchmarkPack(b, "p256", 1024, !unpack) }
func BenchmarkPack256_1Mbyte(b *testing.B) { benchmarkPack(b, "p256", 1024*1024, !unpack) }

func BenchmarkPackUnpack256_1byte(b *testing.B)  { benchmarkPack(b, "p256", 1, unpack) }
func BenchmarkPackUnpack256_1kbyte(b *testing.B) { benchmarkPack(b, "p256", 1024, unpack) }
func BenchmarkPackUnpack256_1Mbyte(b *testing.B) {
	benchmarkPack(b, "p256", 1024*1024, unpack)
}

func TestSharing(t *testing.T) {
	// dude@google.com is the owner of a file that is shared with bob@foo.com.
	const (
		dudesUserName upspin.UserName = "dude@google.com"
		packing                       = upspin.EEPack
		pathName                      = upspin.PathName(dudesUserName + "/secret_file_shared_with_bob")
		bobsUserName  upspin.UserName = "bob@foo.com"
		text                          = "bob, here's the secret file. Sincerely, The Dude."
	)
	dudesPublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	dudesPrivate := "82201047360680847258309465671292633303992565667422607675215625927005262185934\n"
	bobsPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")
	bobsPrivate := "93177533964096447201034856864549483929260757048490326880916443359483929789924"

	// Set up Dude as the creator/owner.
	ctx, packer := setup(dudesUserName, "p256")
	// Set up a mock user service that knows about Dude's public keys (for checking signature during unpack).
	mockUser := &dummyUser{
		userToMatch: []upspin.UserName{dudesUserName},
		keyToReturn: []upspin.PublicKey{dudesPublic},
	}
	f, err := factotum.New(dudesPublic, dudesPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f // Override setup to prevent reading keys from .ssh/
	bind.ReregisterUser(upspin.InProcess, mockUser)
	ctx.User = upspin.Endpoint{
		Transport: upspin.InProcess,
	}

	d := &upspin.DirEntry{
		Name: pathName,
	}
	d.Metadata.Writer = ctx.UserName
	cipher := packBlob(t, ctx, packer, d, []byte(text))
	// Share with Bob
	shareBlob(t, ctx, packer, []upspin.PublicKey{dudesPublic, bobsPublic}, &d.Metadata.Packdata)

	readers, err := packer.ReaderHashes(d.Metadata.Packdata)
	if err != nil {
		t.Fatal(err)
	}
	if len(readers) != 2 {
		t.Errorf("Expected 2 readerhashes, got %d", len(readers))
	}
	hash0 := factotum.KeyHash(dudesPublic)
	hash1 := factotum.KeyHash(bobsPublic)
	if !bytes.Equal(readers[0], hash0) || !bytes.Equal(readers[1], hash1) {
		t.Errorf("text: expected %q; got %q", [][]byte{hash0, hash1}, readers)
	}

	// Now load Bob as the current user.
	ctx.UserName = bobsUserName
	f, err = factotum.New(bobsPublic, bobsPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f

	clear := unpackBlob(t, ctx, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}

	// Finally, check that unpack looked up Dude's public key, to verify the signature.
	if mockUser.returnedKeys != 1 {
		t.Fatal("Packer failed to request dude's public key")
	}
}

func TestBadSharing(t *testing.T) {
	// dudette@google.com is the owner of a file that is attempting to be shared with mia@foo.com, but share wasn't called.
	const (
		dudettesUserName upspin.UserName = "dudette@google.com"
		packing                          = upspin.EEPack
		pathName                         = upspin.PathName(dudettesUserName + "/secret_file_shared_with_mia")
		miasUserName     upspin.UserName = "mia@foo.com"
		text                             = "mia, here's the secret file. sincerely, dudette."
	)
	dudettesPublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	dudettesPrivate := "82201047360680847258309465671292633303992565667422607675215625927005262185934"
	miasPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")
	miasPrivate := "93177533964096447201034856864549483929260757048490326880916443359483929789924"

	ctx, packer := setup(dudettesUserName, "p256")
	mockUser := &dummyUser{
		userToMatch: []upspin.UserName{miasUserName, dudettesUserName},
		keyToReturn: []upspin.PublicKey{miasPublic, dudettesPublic},
	}
	f, err := factotum.New(dudettesPublic, dudettesPrivate) // Override setup to prevent reading keys from .ssh/
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f
	bind.ReregisterUser(upspin.InProcess, mockUser)
	ctx.User = upspin.Endpoint{
		Transport: upspin.InProcess,
	}

	d := &upspin.DirEntry{
		Name: pathName,
	}
	d.Metadata.Writer = ctx.UserName
	cipher := packBlob(t, ctx, packer, d, []byte(text))

	// Don't share with Mia (do nothing).

	// Now load Mia as the current user.
	ctx.UserName = miasUserName
	f, err = factotum.New(miasPublic, miasPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f

	// Mia can't unpack.
	clear := make([]byte, packer.UnpackLen(ctx, cipher, d))
	_, err = packer.Unpack(ctx, clear, cipher, d)
	if err == nil {
		t.Fatal("Expected error, got none.")
	}
	if !strings.Contains(err.Error(), "no wrapped key for me") {
		t.Fatalf("Expected no key error, got %s", err)
	}
}

func setup(name upspin.UserName, curveName string) (*upspin.Context, upspin.Packer) {
	var curve elliptic.Curve
	switch curveName {
	case "p256":
		curve = elliptic.P256()
	case "p384":
		curve = elliptic.P384()
	case "p521":
		curve = elliptic.P521()
	default:
		errors.E("setup", curveName, errors.NotExist, errors.Str("unknown curve"))
	}

	ctx := &upspin.Context{
		UserName: name,
	}
	packer := pack.Lookup(packing)
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		// would be nice to t.Fatal but then can't call from Benchmark?
		panic("ecdsa.GenerateKey failed")
		// return ctx, packer
	}
	kPublic := upspin.PublicKey(fmt.Sprintf("p256\n%s\n%s\n", priv.X.String(), priv.Y.String()))
	kPrivate := fmt.Sprintf("%s\n", priv.D.String())
	ctx.Factotum, err = factotum.New(kPublic, kPrivate)
	if err != nil {
		panic("NewFactotum failed")
	}
	return ctx, packer
}

// dummyUser is a User service that returns a key for a given user.
type dummyUser struct {
	testfixtures.DummyUser
	// The two slices go together
	userToMatch  []upspin.UserName
	keyToReturn  []upspin.PublicKey
	returnedKeys int
}

var _ upspin.User = (*dummyUser)(nil)

func (d *dummyUser) Lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	for i, u := range d.userToMatch {
		if u == userName {
			d.returnedKeys++
			return nil, []upspin.PublicKey{d.keyToReturn[i]}, nil
		}
	}
	return nil, nil, errors.E("Lookup", userName, errors.NotExist, errors.Str("user not found"))
}
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return d, nil
}
