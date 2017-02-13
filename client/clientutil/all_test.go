// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package clientutil

// TODO: test with EEPack; check more error conditions.

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/pack/packutil"
	"upspin.io/test/testfixtures"
	"upspin.io/upspin"

	_ "upspin.io/pack/plain"

	keyserver "upspin.io/key/inprocess"
)

func init() {
	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
}

const (
	userName      = "bob@smith.com"
	aesKeyLen     = 32 // AES-256 because public cloud should withstand multifile multikey attack.
	marshalBufLen = 66 // big enough for p521 according to (c.curve.Params().BitSize + 7) >> 3
)

var (
	inProcess = upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	tLocs []upspin.Location
)

func TestReadAll(t *testing.T) {
	cfg := setupTestConfig(t)
	store := &mockStore{
		locWithContent: tLocs[9],
		content:        []byte("found it!"),
		locRedirection: map[upspin.Reference][]upspin.Location{
			tLocs[0].Reference: []upspin.Location{tLocs[1], tLocs[2], tLocs[3]},
			tLocs[3].Reference: []upspin.Location{tLocs[4], tLocs[2], tLocs[5]},
			tLocs[5].Reference: []upspin.Location{tLocs[6], tLocs[7], tLocs[8]},
			tLocs[8].Reference: []upspin.Location{tLocs[9]},
		},
	}
	err := bind.RegisterStoreServer(upspin.InProcess, store)
	if err != nil {
		t.Fatal(err)
	}
	entry := &upspin.DirEntry{
		Name:       userName + "/testfile",
		SignedName: userName + "/testfile",
		Attr:       upspin.AttrNone,
		Packing:    upspin.PlainPack,
		Blocks: []upspin.DirBlock{
			{
				Offset:   0,
				Size:     int64(len(store.content)),
				Location: tLocs[0],
			},
		},
		Time:     12345,
		Writer:   userName,
		Sequence: upspin.SeqBase,
	}
	dkey := make([]byte, aesKeyLen)
	sum := make([]byte, sha256.Size)
	sig, err := cfg.Factotum().FileSign(entry.Name, entry.Time, dkey, sum)
	if err != nil {
		t.Fatal(err)
	}
	err = pdMarshal(&entry.Packdata, sig, upspin.Signature{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := ReadAll(cfg, entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(store.content) {
		t.Errorf("got = %q, want = %s", got, store.content)
	}
}

func pdMarshal(dst *[]byte, sig, sig2 upspin.Signature) error {
	// sig2 is a signature with another owner key, to enable smoother key rotation
	n := packdataLen()
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	n = 0
	n += packutil.PutBytes((*dst)[n:], sig.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig.S.Bytes())
	if sig2.R == nil {
		zero := big.NewInt(0)
		sig2 = upspin.Signature{R: zero, S: zero}
	}
	n += packutil.PutBytes((*dst)[n:], sig2.R.Bytes())
	n += packutil.PutBytes((*dst)[n:], sig2.S.Bytes())
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
}

// packdataLen returns n big enough for packing, sig.R, sig.S
func packdataLen() int {
	return 2*marshalBufLen + binary.MaxVarintLen64 + 1
}

func setupTestConfig(t *testing.T) upspin.Config {
	// Create some test locations.
	for i := 0; i < 10; i++ {
		loc := upspin.Location{
			Endpoint:  inProcess,
			Reference: upspin.Reference("ref" + string(i)),
		}
		tLocs = append(tLocs, loc)
	}

	f, err := factotum.NewFromDir(repo("bob"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	cfg = config.SetUserName(cfg, userName)
	cfg = config.SetPacking(cfg, upspin.EEPack)
	cfg = config.SetFactotum(cfg, f)
	cfg = config.SetKeyEndpoint(cfg, inProcess)
	cfg = config.SetStoreEndpoint(cfg, inProcess)
	cfg = config.SetDirEndpoint(cfg, inProcess)

	user := &upspin.User{
		Name:      upspin.UserName(userName),
		Dirs:      []upspin.Endpoint{cfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{cfg.StoreEndpoint()},
		PublicKey: f.PublicKey(),
	}
	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	err = key.Put(user)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("client/clientutil: no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/key/testdata/"+dir)
}

type mockStore struct {
	testfixtures.DummyStoreServer
	locWithContent upspin.Location
	content        []byte
	locRedirection map[upspin.Reference][]upspin.Location
}

func (s *mockStore) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	if locs, found := s.locRedirection[ref]; found {
		return nil, nil, locs, nil
	}
	if ref == s.locWithContent.Reference {
		refdata := &upspin.Refdata{
			Reference: ref,
		}
		return s.content, refdata, nil, nil
	}
	return nil, nil, nil, errors.E(errors.NotExist)
}

func (s *mockStore) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}
