package clientutil

import (
	"crypto/rand"
	"crypto/sha256"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/test/testfixtures"
	"upspin.io/upspin"
)

func BenchmarkReadAll(b *testing.B) {
	latency := 50 * time.Millisecond
	cfg, _, entry := setupBenchmark(b, 50<<20, latency)

	for i := 0; i < b.N; i++ {
		got, err := ReadAll(cfg, entry)
		if err != nil {
			b.Fatal(err)
		}
		_ = got
	}
}

var (
	remote = upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   "", // ignored
	}
)

var bindOnce sync.Once

func setupBenchmark(b *testing.B, size int, latency time.Duration) (upspin.Config, *mockSlowStore, *upspin.DirEntry) {
	cfg := setupTestConfig(b)

	content := make([]byte, size)
	rand.Read(content)

	store := &mockSlowStore{
		content: content,
		latency: latency,
	}

	// TODO: this is wrong. All the setupBenchmark is very hacky...
	bindOnce.Do(func() {
		// Bind to Remote because inprocess is already used.
		err := bind.RegisterStoreServer(upspin.Remote, store)
		if err != nil {
			b.Fatal(err)
		}
	})

	entry := &upspin.DirEntry{
		Name:       userName + "/testfile",
		SignedName: userName + "/testfile",
		Attr:       upspin.AttrNone,
		Packing:    upspin.PlainPack,
		Time:       12345,
		Writer:     userName,
		Sequence:   upspin.SeqBase,
	}

	offset := 0
	for i := 0; offset < len(store.content); i++ {
		loc := upspin.Location{
			Endpoint:  remote,
			Reference: upspin.Reference("ref" + strconv.Itoa(i)),
		}
		b := upspin.DirBlock{
			Offset:   int64(offset),
			Size:     int64(min(upspin.BlockSize, len(store.content)-offset)),
			Location: loc,
		}
		entry.Blocks = append(entry.Blocks, b)
		offset += upspin.BlockSize
	}

	f := cfg.Factotum()
	dkey := make([]byte, aesKeyLen)
	sum := make([]byte, sha256.Size)
	vhash := f.DirEntryHash(entry.SignedName, entry.Link, entry.Attr, entry.Packing, entry.Time, dkey, sum)
	sig, err := f.FileSign(vhash)
	if err != nil {
		b.Fatal(err)
	}
	err = pdMarshal(&entry.Packdata, sig, upspin.Signature{})
	if err != nil {
		b.Fatal(err)
	}

	return cfg, store, entry
}

type mockSlowStore struct {
	testfixtures.DummyStoreServer
	content []byte
	latency time.Duration
}

func (s *mockSlowStore) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	time.Sleep(s.latency)

	n := strings.TrimPrefix(string(ref), "ref")
	number, err := strconv.Atoi(n)
	if err != nil || number < 0 {
		return nil, nil, nil, errors.E(errors.NotExist)
	}

	offset := number * upspin.BlockSize
	if offset >= len(s.content) {
		return nil, nil, nil, errors.E(errors.NotExist)
	}

	end := min(offset+upspin.BlockSize, len(s.content))
	refdata := &upspin.Refdata{
		Reference: ref,
	}
	return s.content[offset:end], refdata, nil, nil
}

func (s *mockSlowStore) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
