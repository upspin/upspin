// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ee_test

import (
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"log"
	"strings"
	"testing"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/pack"
	"upspin.io/pack/ee"
	"upspin.io/pack/internal/packtest"
	"upspin.io/test/testfixtures"
	"upspin.io/test/testutil"
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
func packBlob(t *testing.T, cfg upspin.Config, packer upspin.Packer, d *upspin.DirEntry, text []byte) []byte {
	d.Packing = packer.Packing()
	bp, err := packer.Pack(cfg, d)
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
	if d.SignedName != d.Name {
		t.Errorf("SignedName: expected %q; got %q", d.Name, d.SignedName)
	}
}

func testPackNameAndUnpack(t *testing.T, cfg upspin.Config, packer upspin.Packer, name, newName upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Writer:     cfg.UserName(),
	}
	cipher := packBlob(t, cfg, packer, d, text)

	// Name to newName.
	if err := packer.Name(cfg, d, newName); err != nil {
		t.Errorf("Name failed: %s", err)
	}
	if d.Name != newName {
		t.Errorf("Name failed to set the name")
	}

	// Now unpack.
	clear := unpackBlob(t, cfg, packer, d, cipher)

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
	cfg, packer := setup(user)
	testPackAndUnpack(t, cfg, packer, name, []byte(text))
}

func TestName256(t *testing.T) {
	const (
		user    upspin.UserName = "joe@upspin.io"
		name                    = upspin.PathName(user + "/file/of/user.256")
		newName                 = upspin.PathName(user + "/file/of/user.256.2")
		text                    = "this is some text 256"
	)
	cfg, packer := setup(user)
	testPackNameAndUnpack(t, cfg, packer, name, newName, []byte(text))
}

func benchmarkPack(b *testing.B, curveName string, fileSize int, unpack bool) {
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

func BenchmarkPack256_1byte(b *testing.B)  { benchmarkPack(b, "p256", 1, !unpack) }
func BenchmarkPack256_1kbyte(b *testing.B) { benchmarkPack(b, "p256", 1024, !unpack) }
func BenchmarkPack256_1Mbyte(b *testing.B) { benchmarkPack(b, "p256", 1024*1024, !unpack) }

func BenchmarkPackUnpack256_1byte(b *testing.B)  { benchmarkPack(b, "p256", 1, unpack) }
func BenchmarkPackUnpack256_1kbyte(b *testing.B) { benchmarkPack(b, "p256", 1024, unpack) }
func BenchmarkPackUnpack256_1Mbyte(b *testing.B) {
	benchmarkPack(b, "p256", 1024*1024, unpack)
}

// shareBlob updates the packdata of a blob such that the public keys given are readers of the blob.
func shareBlob(t *testing.T, cfg upspin.Config, packer upspin.Packer, readers []upspin.PublicKey, packdata *[]byte) {
	pd := make([]*[]byte, 1)
	pd[0] = packdata
	packer.Share(cfg, readers, pd)
}

func TestSharing(t *testing.T) {
	// TODO This could be cleaned up to be more like TestCountersign.
	// joe@google.com is the owner of a file that is shared with bob@foo.com.
	const (
		joesUserName   upspin.UserName = "joe@upspin.io"
		pathName                       = upspin.PathName(joesUserName + "/secret_file_shared_with_bob")
		bobsUserName   upspin.UserName = "bob@upspin.io"
		carlasUserName upspin.UserName = "carla@baz.edu"
		text                           = "bob, here's the secret file. Sincerely, The Joe."
	)
	joePublic := upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	bobPublic := upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528\n")
	carlaPublic := upspin.PublicKey("p384\n26172614276096813357206176213406982397222536659671409755310805362042028026922579207014531049688734331134000100158544\n17028658482487767962568267600820350664652897469525797908053707470805274016916949610485516295521856564391853226932191\n")

	// Set up Joe as the creator/owner.
	joecfg, packer := setup(joesUserName)

	d := &upspin.DirEntry{
		Name:       pathName,
		SignedName: pathName,
	}
	d.Writer = joecfg.UserName()
	cipher := packBlob(t, joecfg, packer, d, []byte(text))
	// Share with Bob and Carla.
	shareBlob(t, joecfg, packer, []upspin.PublicKey{joePublic, bobPublic, carlaPublic}, &d.Packdata)

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
	bobcfg, packer := setup(bobsUserName)
	bobcfg = config.SetKeyEndpoint(bobcfg, upspin.Endpoint{Transport: upspin.InProcess})
	clear := unpackBlob(t, bobcfg, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}

	// Load Carla as the current user.
	carlacfg, packer := setup(carlasUserName)
	carlacfg = config.SetKeyEndpoint(carlacfg, upspin.Endpoint{Transport: upspin.InProcess})
	clear = unpackBlob(t, carlacfg, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}
}

func TestBadSharing(t *testing.T) {
	// joe@google.com is the owner of a file that is attempting to be shared with bob@foo.com, but share wasn't called.
	const (
		joesUserName upspin.UserName = "joe@upspin.io"
		pathName                     = upspin.PathName(joesUserName + "/secret_file_shared_with_bob")
		bobsUserName upspin.UserName = "bob@upspin.io"
		text                         = "bob, here's the secret file. sincerely, joe."
	)
	cfg, packer := setup(joesUserName)

	d := &upspin.DirEntry{
		Name:       pathName,
		SignedName: pathName,
	}
	d.Writer = cfg.UserName()
	packBlob(t, cfg, packer, d, []byte(text))

	// Don't share with Bob (do nothing).

	// Now load Bob as the current user.
	cfg = config.SetUserName(cfg, bobsUserName)
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "bob"))
	if err != nil {
		t.Fatal(err)
	}
	cfg = config.SetFactotum(cfg, f)

	// Bob can't unpack.
	_, err = packer.Unpack(cfg, d)
	if err == nil {
		t.Fatal("Expected error, got none.")
	}
	if !errors.Is(errors.CannotDecrypt, err) {
		t.Fatalf("Expected CannotDecrypt error, got %s", err)
	}
}

func TestCountersign(t *testing.T) {
	const (
		joeUserName upspin.UserName = "joe@upspin.io"
		bobUserName upspin.UserName = "bob@upspin.io"
		pathName                    = upspin.PathName(joeUserName + "/secret_for_bob")
		text                        = "bob, here's the secret file. Sincerely, The Joe."
	)
	joeConfig, _ := setup(joeUserName)
	joePublic := joeConfig.Factotum().PublicKey()
	bobConfig, packer := setup(bobUserName)
	bobPublic := bobConfig.Factotum().PublicKey()
	bobConfig = config.SetKeyEndpoint(bobConfig, upspin.Endpoint{Transport: upspin.InProcess})

	// Share file with Bob.
	d := &upspin.DirEntry{
		Name:       pathName,
		SignedName: pathName,
	}
	d.Writer = joeConfig.UserName()
	cipher := packBlob(t, joeConfig, packer, d, []byte(text))
	shareBlob(t, joeConfig, packer, []upspin.PublicKey{joePublic, bobPublic}, &d.Packdata)

	// Emulate Joe executing "upspin keygen -rotate".
	f2, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "joe2"))
	if err != nil {
		t.Fatalf("cannot create second (key-rotated) factotum for joe: %v", err)
	}
	joeConfig = config.SetFactotum(joeConfig, f2)

	// We know from TestSharing that Bob can read. Try again with Countersign.
	err = packer.Countersign(joePublic, joeConfig.Factotum(), d)
	if err != nil {
		t.Fatal(err)
	}
	clear := unpackBlob(t, bobConfig, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %q, got %q", text, clear)
	}

	// And yet again, after emulating Joe executing "upspin rotate".
	clear = unpackBlob(t, bobConfig, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %q, got %q", text, clear)
	}
}

func cfgFor(name upspin.UserName) (upspin.Config, upspin.Packer) {
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
	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{Transport: upspin.InProcess})
	return cfg, packer
}

func setup(name upspin.UserName) (upspin.Config, upspin.Packer) {
	cfg, packer := cfgFor(name)

	joeCfg, _ := cfgFor("joe@upspin.io")
	bobCfg, _ := cfgFor("bob@upspin.io")
	mockKey := &dummyKey{
		userToMatch: []upspin.UserName{
			joeCfg.UserName(),
			bobCfg.UserName(),
		},
		keyToReturn: []upspin.PublicKey{
			joeCfg.Factotum().PublicKey(),
			bobCfg.Factotum().PublicKey(),
		},
	}
	bind.RegisterKeyServer(upspin.InProcess, mockKey)
	return cfg, packer
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
	const op errors.Op = "pack/ee.dummyKey.Lookup"
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
	return nil, errors.E(op, userName, errors.NotExist, "user not found")
}
func (d *dummyKey) Dial(cc upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	return d, nil
}

func TestMultiBlockRoundTrip(t *testing.T) {
	const userName = upspin.UserName("aly@upspin.io")
	cfg, packer := setup(userName)
	packtest.TestMultiBlockRoundTrip(t, cfg, packer, userName)
}

func TestConsistentKeyStream(t *testing.T) {
	// This test that the EE packer with different block sizes still
	// generates the same ciphertext when all blocks are concatenated.
	blockSizes := []int{777, 1024, 4001, 92341, 1024 * 1024}
	const (
		user upspin.UserName = "joe@upspin.io"
		name                 = upspin.PathName(user + "/file/of/user")
	)

	cfg, packer := setup(user)
	de := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Writer:     cfg.UserName(),
		Packing:    packer.Packing(),
	}

	// Generate a little over 2MB of random data.
	data := make([]byte, 2*1024*1024+3)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}

	// Create a new key to re-use for each separate pack operation.
	dkey, blockCipher, err := ee.NewKeyAndCipher()
	if err != nil {
		t.Fatal(err)
	}

	// Generate the expected ciphertext in one operation.
	// We will then compare the ciphertext generated over multiple blocks
	// against this canonical reference.
	wantCipherText := make([]byte, len(data))
	iv := make([]byte, blockCipher.BlockSize()) // zero
	cipher.NewCTR(blockCipher, iv).XORKeyStream(wantCipherText, data)

	// Encrypt data at various block sizes.
	dirEntries := map[int]upspin.DirEntry{}
	for _, bs := range blockSizes {
		t.Logf("encrypt blockSize=%d", bs)

		bp, err := packer.Pack(cfg, de)
		if err != nil {
			t.Fatal(err)
		}

		// Replace the random dkey/cipher with our own.
		// Pass a copy of dkey, as the original will get zeroed on close.
		ee.SetblockPacker(bp, append([]byte(nil), dkey...), blockCipher)

		var gotCipherText []byte
		for i := 0; i < len(data); i += bs {
			clear := data[i:]
			if len(clear) > bs {
				clear = clear[:bs]
			}
			cipher, err := bp.Pack(clear)
			if err != nil {
				t.Fatal(err)
			}
			gotCipherText = append(gotCipherText, cipher...)
			bp.SetLocation(upspin.Location{Reference: "dummy"})
		}
		if err := bp.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotCipherText, wantCipherText) {
			t.Fatalf("cipherText for block size %d did not match", bs)
		}

		dirEntries[bs] = *de
		de.Packdata = nil
		de.Blocks = nil
	}

	// Decrypt data and verify.
	for _, bs := range blockSizes {
		t.Logf("decrypt blockSize=%d", bs)

		de := dirEntries[bs]
		bu, err := packer.Unpack(cfg, &de)
		if err != nil {
			t.Fatal(err)
		}

		got := make([]byte, len(data))

		for i := 0; i < len(data); i += bs {
			if _, ok := bu.NextBlock(); !ok {
				t.Fatal("expected next block, didn't find one")
			}
			cipher := wantCipherText[i:]
			if len(cipher) > bs {
				cipher = cipher[:bs]
			}
			clear, err := bu.Unpack(cipher)
			if err != nil {
				t.Fatal(err)
			}
			copy(got[i:], clear)
		}

		if !bytes.Equal(data, got) {
			t.Errorf("cleartext for blockSize=%d does not match input", bs)
		}
	}
}

func TestAllReaders(t *testing.T) {
	const (
		userName  = upspin.UserName("joe@upspin.io")
		otherName = upspin.UserName("aly@upspin.io")
		pathName  = upspin.PathName(userName + "/dir/file")
		content   = "Some text"
	)

	cfg, packer := setup(userName)
	cfg2, _ := setup(otherName)

	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{Transport: upspin.InProcess})
	cfg2 = config.SetKeyEndpoint(cfg2, upspin.Endpoint{Transport: upspin.InProcess})

	de := &upspin.DirEntry{
		Name:       pathName,
		SignedName: pathName,
		Writer:     userName,
		Packing:    packer.Packing(),
	}

	cipher := packBlob(t, cfg, packer, de, []byte(content))

	ok, err := packer.UnpackableByAll(de)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("UnpackableByAll returned true, want false")
	}

	if _, err := packer.Unpack(cfg2, de); err == nil {
		t.Fatalf("expected error unpacking as %s, got nil", otherName)
	}

	readers := []upspin.PublicKey{
		cfg.Factotum().PublicKey(),
		upspin.AllUsersKey,
	}
	packer.Share(cfg, readers, []*[]byte{&de.Packdata})

	ok, err = packer.UnpackableByAll(de)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("UnpackableByAll returned false, want true")
	}

	bp, err := packer.Unpack(cfg2, de)
	if err != nil {
		t.Fatalf("error unpacking as %s: %v", otherName, err)
	}
	if _, ok := bp.NextBlock(); !ok {
		t.Fatalf("error unpacking as %s: %v", otherName, err)
	}
	clear, err := bp.Unpack(cipher)
	if err != nil {
		t.Fatalf("error unpacking as %s: %v", otherName, err)
	}

	if got, want := string(clear), content; got != want {
		t.Errorf("content unpacked as %q, want %q", got, want)
	}
}
