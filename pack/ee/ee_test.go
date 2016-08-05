// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/pack/internal/packtest"
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
func packBlob(t *testing.T, ctx upspin.Context, packer upspin.Packer, d *upspin.DirEntry, text []byte) []byte {
	d.Packing = packer.Packing()
	bp, err := packer.Pack(ctx, d)
	if err != nil {
		t.Fatal("packBlob:", err)
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
func unpackBlob(t *testing.T, ctx upspin.Context, packer upspin.Packer, d *upspin.DirEntry, cipher []byte) []byte {
	bp, err := packer.Unpack(ctx, d)
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

func testPackAndUnpack(t *testing.T, ctx upspin.Context, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{}
	d.Name = name
	d.Writer = ctx.UserName()
	cipher := packBlob(t, ctx, packer, d, text)

	// Now unpack.
	clear := unpackBlob(t, ctx, packer, d, cipher)

	if !bytes.Equal(text, clear) {
		t.Errorf("text: expected %q; got %q", text, clear)
	}
}

func testPackNameAndUnpack(t *testing.T, ctx upspin.Context, packer upspin.Packer, name, newName upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{}
	d.Name = name
	d.Writer = ctx.UserName()
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
		user upspin.UserName = "joe@upspin.io"
		name                 = upspin.PathName(user + "/file/of/user.256")
		text                 = "this is some text 256"
	)
	ctx, packer := setup(user)
	testPackAndUnpack(t, ctx, packer, name, []byte(text))
}

func TestName256(t *testing.T) {
	const (
		user    upspin.UserName = "joe@upspin.io"
		name                    = upspin.PathName(user + "/file/of/user.256")
		newName                 = upspin.PathName(user + "/file/of/user.256.2")
		text                    = "this is some text 256"
	)
	ctx, packer := setup(user)
	testPackNameAndUnpack(t, ctx, packer, name, newName, []byte(text))
}

func benchmarkPack(b *testing.B, curveName string, fileSize int, unpack bool) {
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
	ctx, packer := setup(user)
	for i := 0; i < b.N; i++ {
		d := &upspin.DirEntry{
			Name:    name,
			Writer:  ctx.UserName(),
			Packing: packer.Packing(),
		}
		bp, err := packer.Pack(ctx, d)
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
		bu, err := packer.Unpack(ctx, d)
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

func BenchmarkPack256_1byte(b *testing.B)  { benchmarkPack(b, "p256", 1, !unpack) }
func BenchmarkPack256_1kbyte(b *testing.B) { benchmarkPack(b, "p256", 1024, !unpack) }
func BenchmarkPack256_1Mbyte(b *testing.B) { benchmarkPack(b, "p256", 1024*1024, !unpack) }

func BenchmarkPackUnpack256_1byte(b *testing.B)  { benchmarkPack(b, "p256", 1, unpack) }
func BenchmarkPackUnpack256_1kbyte(b *testing.B) { benchmarkPack(b, "p256", 1024, unpack) }
func BenchmarkPackUnpack256_1Mbyte(b *testing.B) {
	benchmarkPack(b, "p256", 1024*1024, unpack)
}

// shareBlob updates the packdata of a blob such that the public keys given are readers of the blob.
func shareBlob(t *testing.T, ctx upspin.Context, packer upspin.Packer, readers []upspin.PublicKey, packdata *[]byte) {
	pd := make([]*[]byte, 1)
	pd[0] = packdata
	packer.Share(ctx, readers, pd)
}

func TestSharing(t *testing.T) {
	// joe@google.com is the owner of a file that is shared with bob@foo.com.
	const (
		joesUserName   upspin.UserName = "joe@google.com"
		pathName                       = upspin.PathName(joesUserName + "/secret_file_shared_with_bob")
		bobsUserName   upspin.UserName = "bob@foo.com"
		carlasUserName upspin.UserName = "carla@baz.edu"
		text                           = "bob, here's the secret file. Sincerely, The Joe."
	)
	joePublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	bobPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")
	carlaPublic := upspin.PublicKey("p384\n26172614276096813357206176213406982397222536659671409755310805362042028026922579207014531049688734331134000100158544\n17028658482487767962568267600820350664652897469525797908053707470805274016916949610485516295521856564391853226932191\n")

	// Set up Joe as the creator/owner.
	joectx, packer := setup(joesUserName)
	// Set up a mock user service that knows about Joe's public keys (for checking signature during unpack).
	mockKey := &dummyKey{
		userToMatch: []upspin.UserName{joesUserName},
		keyToReturn: []upspin.PublicKey{joectx.Factotum().PublicKey()},
	}
	bind.ReregisterKeyServer(upspin.InProcess, mockKey)
	joectx.SetKeyEndpoint(upspin.Endpoint{Transport: upspin.InProcess})

	d := &upspin.DirEntry{
		Name: pathName,
	}
	d.Writer = joectx.UserName()
	cipher := packBlob(t, joectx, packer, d, []byte(text))
	// Share with Bob and Carla.
	shareBlob(t, joectx, packer, []upspin.PublicKey{joePublic, bobPublic, carlaPublic}, &d.Packdata)

	readers, err := packer.ReaderHashes(d.Packdata)
	if err != nil {
		t.Fatal(err)
	}
	if len(readers) != 3 {
		t.Errorf("Expected 3 readerhashes, got %d", len(readers))
	}
	hash0 := factotum.KeyHash(joePublic)
	hash1 := factotum.KeyHash(bobPublic)
	hash2 := factotum.KeyHash(carlaPublic)
	if !bytes.Equal(readers[0], hash0) || !bytes.Equal(readers[1], hash1) || !bytes.Equal(readers[2], hash2) {
		t.Errorf("text: expected %q; got %q", [][]byte{hash0, hash1, hash2}, readers)
	}

	// Now load Bob as the current user.
	bobctx, packer := setup(bobsUserName)
	bobctx.SetKeyEndpoint(upspin.Endpoint{Transport: upspin.InProcess})
	clear := unpackBlob(t, bobctx, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}

	// Finally, check that unpack looked up Joe's public key, to verify the signature.
	if mockKey.returnedKeys != 1 {
		t.Fatal("Packer failed to request dude's public key")
	}

	// Load Carla as the current user.
	carlactx, packer := setup(carlasUserName)
	carlactx.SetKeyEndpoint(upspin.Endpoint{Transport: upspin.InProcess})
	clear = unpackBlob(t, carlactx, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}
}

func TestBadSharing(t *testing.T) {
	// joe@google.com is the owner of a file that is attempting to be shared with bob@foo.com, but share wasn't called.
	const (
		joesUserName upspin.UserName = "joe@google.com"
		pathName                     = upspin.PathName(joesUserName + "/secret_file_shared_with_bob")
		bobsUserName upspin.UserName = "bob@foo.com"
		text                         = "bob, here's the secret file. sincerely, joe."
	)
	joePublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	bobPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")

	ctx, packer := setup(joesUserName)
	mockKey := &dummyKey{
		userToMatch: []upspin.UserName{bobsUserName, joesUserName},
		keyToReturn: []upspin.PublicKey{bobPublic, joePublic},
	}
	f, err := factotum.New(repo("key/testdata/joe"))
	if err != nil {
		t.Fatal(err)
	}
	ctx.SetFactotum(f)
	bind.ReregisterKeyServer(upspin.InProcess, mockKey)
	ctx.SetKeyEndpoint(upspin.Endpoint{
		Transport: upspin.InProcess,
	})

	d := &upspin.DirEntry{
		Name: pathName,
	}
	d.Writer = ctx.UserName()
	packBlob(t, ctx, packer, d, []byte(text))

	// Don't share with Bob (do nothing).

	// Now load Bob as the current user.
	ctx.SetUserName(bobsUserName)
	f, err = factotum.New(repo("key/testdata/bob"))
	if err != nil {
		t.Fatal(err)
	}
	ctx.SetFactotum(f)

	// Bob can't unpack.
	_, err = packer.Unpack(ctx, d)
	if err == nil {
		t.Fatal("Expected error, got none.")
	}
	if !strings.Contains(err.Error(), "could not find wrapped key") {
		t.Fatalf("Expected no key error, got %s", err)
	}
}

func setup(name upspin.UserName) (upspin.Context, upspin.Packer) {
	ctx := context.New().SetUserName(name)
	packer := pack.Lookup(packing)
	j := strings.IndexByte(string(name), '@')
	if j < 0 {
		log.Fatal("malformed username %s", name)
	}
	f, err := factotum.New(repo("key/testdata/" + string(name[:j])))
	if err != nil {
		log.Fatal("unable to initialize factotum for %s", string(name[:j]))
	}
	ctx.SetFactotum(f)
	return ctx, packer
}

// dummyKey is a User service that returns a key for a given user.
type dummyKey struct {
	testfixtures.DummyKey
	// The two slices go together
	userToMatch  []upspin.UserName
	keyToReturn  []upspin.PublicKey
	returnedKeys int
}

var _ upspin.KeyServer = (*dummyKey)(nil)

func (d *dummyKey) Lookup(userName upspin.UserName) (*upspin.User, error) {
	for i, u := range d.userToMatch {
		if u == userName {
			d.returnedKeys++
			user := &upspin.User{
				Name:      userName,
				PublicKey: d.keyToReturn[i],
			}
			return user, nil
		}
	}
	return nil, errors.E("Lookup", userName, errors.NotExist, errors.Str("user not found"))
}
func (d *dummyKey) Dial(cc upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return d, nil
}

func TestMultiBlockRoundTrip(t *testing.T) {
	const userName = upspin.UserName("aly@upspin.io")
	ctx, packer := setup(userName)
	packtest.TestMultiBlockRoundTrip(t, ctx, packer, userName)
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
