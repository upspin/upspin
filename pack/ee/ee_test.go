// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	mRand "math/rand"
	"strings"
	"testing"

	"upspin.io/bind"
	"upspin.io/context"
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
	// dude@google.com is the owner of a file that is shared with bob@foo.com.
	const (
		dudesUserName  upspin.UserName = "dude@google.com"
		pathName                       = upspin.PathName(dudesUserName + "/secret_file_shared_with_bob")
		bobsUserName   upspin.UserName = "bob@foo.com"
		carlasUserName upspin.UserName = "carla@baz.edu"
		text                           = "bob, here's the secret file. Sincerely, The Dude."
	)
	dudesPublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	dudesPrivate := "82201047360680847258309465671292633303992565667422607675215625927005262185934\n"
	bobsPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")
	bobsPrivate := "93177533964096447201034856864549483929260757048490326880916443359483929789924"
	carlasPublic := upspin.PublicKey("p384\n26172614276096813357206176213406982397222536659671409755310805362042028026922579207014531049688734331134000100158544\n17028658482487767962568267600820350664652897469525797908053707470805274016916949610485516295521856564391853226932191\n")
	carlasPrivate := "30201512592735536590793019705840595870765268847836648868491872481691553233567108528485588759694229643034052691415730"
	// Carla's keys can be regenerated with "keygen -secretseed vutus-pohud-kagaf-tugag.kumal-hoduz-duzin-pafip".

	// Set up Dude as the creator/owner.
	ctx, packer := setup(dudesUserName, "p256")
	// Set up a mock user service that knows about Dude's public keys (for checking signature during unpack).
	mockKey := &dummyKey{
		userToMatch: []upspin.UserName{dudesUserName},
		keyToReturn: []upspin.PublicKey{dudesPublic},
	}
	f, err := factotum.New(dudesPublic, dudesPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.SetFactotum(f) // Override setup to prevent reading keys from .ssh/
	bind.ReregisterKeyServer(upspin.InProcess, mockKey)
	ctx.SetKeyEndpoint(upspin.Endpoint{Transport: upspin.InProcess})

	d := &upspin.DirEntry{
		Name: pathName,
	}
	d.Writer = ctx.UserName()
	cipher := packBlob(t, ctx, packer, d, []byte(text))
	// Share with Bob and Carla.
	shareBlob(t, ctx, packer, []upspin.PublicKey{dudesPublic, bobsPublic, carlasPublic}, &d.Packdata)

	readers, err := packer.ReaderHashes(d.Packdata)
	if err != nil {
		t.Fatal(err)
	}
	if len(readers) != 3 {
		t.Errorf("Expected 3 readerhashes, got %d", len(readers))
	}
	hash0 := factotum.KeyHash(dudesPublic)
	hash1 := factotum.KeyHash(bobsPublic)
	hash2 := factotum.KeyHash(carlasPublic)
	if !bytes.Equal(readers[0], hash0) || !bytes.Equal(readers[1], hash1) || !bytes.Equal(readers[2], hash2) {
		t.Errorf("text: expected %q; got %q", [][]byte{hash0, hash1, hash2}, readers)
	}

	// Now load Bob as the current user.
	ctx.SetUserName(bobsUserName)
	f, err = factotum.New(bobsPublic, bobsPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.SetFactotum(f)

	clear := unpackBlob(t, ctx, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}

	// Finally, check that unpack looked up Dude's public key, to verify the signature.
	if mockKey.returnedKeys != 1 {
		t.Fatal("Packer failed to request dude's public key")
	}

	// Load Carla as the current user.
	ctx.SetUserName(carlasUserName)
	f, err = factotum.New(carlasPublic, carlasPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.SetFactotum(f)

	clear = unpackBlob(t, ctx, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}
}

func TestBadSharing(t *testing.T) {
	// dudette@google.com is the owner of a file that is attempting to be shared with mia@foo.com, but share wasn't called.
	const (
		dudettesUserName upspin.UserName = "dudette@google.com"
		pathName                         = upspin.PathName(dudettesUserName + "/secret_file_shared_with_mia")
		miasUserName     upspin.UserName = "mia@foo.com"
		text                             = "mia, here's the secret file. sincerely, dudette."
	)
	dudettesPublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	dudettesPrivate := "82201047360680847258309465671292633303992565667422607675215625927005262185934"
	miasPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")
	miasPrivate := "93177533964096447201034856864549483929260757048490326880916443359483929789924"

	ctx, packer := setup(dudettesUserName, "p256")
	mockKey := &dummyKey{
		userToMatch: []upspin.UserName{miasUserName, dudettesUserName},
		keyToReturn: []upspin.PublicKey{miasPublic, dudettesPublic},
	}
	f, err := factotum.New(dudettesPublic, dudettesPrivate) // Override setup to prevent reading keys from .ssh/
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

	// Don't share with Mia (do nothing).

	// Now load Mia as the current user.
	ctx.SetUserName(miasUserName)
	f, err = factotum.New(miasPublic, miasPrivate)
	if err != nil {
		t.Fatal(err)
	}
	ctx.SetFactotum(f)

	// Mia can't unpack.
	_, err = packer.Unpack(ctx, d)
	if err == nil {
		t.Fatal("Expected error, got none.")
	}
	if !strings.Contains(err.Error(), "could not find wrapped key") {
		t.Fatalf("Expected no key error, got %s", err)
	}
}

func setup(name upspin.UserName, curveName string) (upspin.Context, upspin.Packer) {
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

	ctx := context.New().SetUserName(name)
	packer := pack.Lookup(packing)
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		// would be nice to t.Fatal but then can't call from Benchmark?
		panic("ecdsa.GenerateKey failed")
		// return ctx, packer
	}
	public := upspin.PublicKey(fmt.Sprintf("p256\n%s\n%s\n", priv.X.String(), priv.Y.String()))
	private := fmt.Sprintf("%s\n", priv.D.String())
	f, err := factotum.New(public, private)
	if err != nil {
		panic("NewFactotum failed")
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

type fakeStore map[upspin.Reference][]byte

func TestMultiBlockRoundTrip(t *testing.T) {
	const (
		userName = upspin.UserName("ken@google.com")
		pathName = upspin.PathName(userName + "/file")
	)

	ctx, packer := setup(userName, "p256")

	// Work with 1MB of random data.
	data := make([]byte, 1<<20)
	n, err := rand.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data) {
		t.Fatalf("read %v bytes, want %v", n, len(data))
	}

	de := &upspin.DirEntry{
		Name:    pathName,
		Writer:  userName,
		Packing: packer.Packing(),
	}

	store := make(fakeStore)

	if err := packEntry(ctx, store, packer, de, bytes.NewReader(data)); err != nil {
		t.Fatal("packEntry:", err)
	}

	t.Logf("packed %v bytes into %v blocks", len(data), len(de.Blocks))

	var out bytes.Buffer
	if err := unpackEntry(ctx, store, packer, de, &out); err != nil {
		t.Fatal("unpackEntry:", err)
	}

	t.Logf("unpacked %v bytes", out.Len())

	if !bytes.Equal(data, out.Bytes()) {
		t.Fatal("output did not match input")
	}
}

func packEntry(ctx upspin.Context, store fakeStore, packer upspin.Packer, de *upspin.DirEntry, r io.Reader) error {
	bp, err := packer.Pack(ctx, de)
	if err != nil {
		return err
	}

	rand := mRand.New(mRand.NewSource(1))

	// Store and pack data in 1KB increments.
	clear := make([]byte, 1<<10)
loop:
	for {
		// Pick a pseudo-random block size.
		clear = clear[:rand.Intn(cap(clear)-1)+1]

		n, err := io.ReadFull(r, clear)
		switch err {
		case nil, io.ErrUnexpectedEOF:
			// OK
		case io.EOF:
			break loop
		default:
			// Handle read error.
			return err
		}

		cipher, err := bp.Pack(clear[:n])
		if err != nil {
			return err
		}

		// Store the ciphertext, creating a pseudo-ref.
		sum := sha256.Sum256(cipher)
		ref := upspin.Reference(fmt.Sprintf("%x", sum))
		store[ref] = append([]byte(nil), cipher...)

		bp.SetLocation(upspin.Location{Reference: ref})
	}

	return bp.Close()
}

func unpackEntry(ctx upspin.Context, store fakeStore, packer upspin.Packer, de *upspin.DirEntry, w io.Writer) error {
	bp, err := packer.Unpack(ctx, de)
	if err != nil {
		return err
	}

	for {
		b, ok := bp.NextBlock()
		if !ok {
			return nil
		}

		ref := b.Location.Reference
		cipher, ok := store[ref]
		if !ok {
			return fmt.Errorf("could not find reference %q in store", ref)
		}

		clear, err := bp.Unpack(cipher)
		if err != nil {
			return err
		}

		if _, err := w.Write(clear); err != nil {
			return err
		}
	}
	return nil
}
