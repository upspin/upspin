// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factotum

import (
	"path/filepath"
	"testing"

	"upspin.io/upspin"
)

func TestNewFromDir(t *testing.T) {
	const (
		pubKey    = "p256\n86754568856409436056886548963722747418663925733852968840719951502625645703023\n55374006944977701639377273685946154797448684848748065688191847332792959379206\n"
		secKey    = "33732563467898584041325590158539299810645722675081856412396066039103123277092\n"
		newPubKey = "p256\n6640270742675236934700552659758623510932789581985633007789325329362331148012\n68892645101823987570169861213316538980647268870890981023717754447508722389034\n"
		newSecKey = "73412709577437621283953284627141522517131750837511539431619352194608555895350\n"
	)

	cases := []struct {
		dir        string
		ok         bool
		public     upspin.PublicKey
		secret     string
		prevPublic upspin.PublicKey
		prevSecret string
	}{
		// Check that basic key parsing and parsing of archived keys works.
		{"ok", true, pubKey, secKey, "", ""},
		{"ok-archived", true, newPubKey, newSecKey, pubKey, secKey},
		// When we fail to parse the archived keys
		// we should see the current key as the previous key.
		{"bad-archived", true, newPubKey, newSecKey, newPubKey, newSecKey},
		// These should outright fail.
		{"bad", false, "", "", "", ""},
		{"empty", false, "", "", "", ""},
		{"mismatched", false, pubKey, secKey, "", ""},
	}
	for _, c := range cases {
		fi, err := NewFromDir(filepath.Join("testdata", c.dir))
		if err != nil {
			if c.ok {
				t.Errorf("NewFromDir(%q): %v", c.dir, err)
			}
			continue
		}
		if !c.ok {
			t.Errorf("NewFromDir(%q) returned nil error, expected error", c.dir)
			continue
		}
		f := fi.(*factotum)
		if got, want := f.keys[f.current].public, c.public; got != want {
			t.Errorf("NewFromDir(%q): got public key %q, want %q", c.dir, got, want)
		}
		if got, want := f.keys[f.current].private, c.secret; got != want {
			t.Errorf("NewFromDir(%q): got secret key %q, want %q", c.dir, got, want)
		}
		if c.prevPublic == "" {
			if f.current != f.previous {
				t.Errorf("NewFromDir(%q): expected no previous key, got %s", c.dir, f.previous)
			}
			continue
		}
		if got, want := f.keys[f.previous].public, c.prevPublic; got != want {
			t.Errorf("NewFromDir(%q): got previous public key %q, want %q", c.dir, got, want)
		}
		if got, want := f.keys[f.previous].private, c.prevSecret; got != want {
			t.Errorf("NewFromDir(%q): got previous secret key %q, want %q", c.dir, got, want)
		}
	}
}

func TestClean(t *testing.T) {
	f, err := NewFromDir(filepath.Join("testdata", "ok"))
	if err != nil {
		t.Errorf("NewFromDir(testdata/ok): %v", err)
	}
	fi1 := f.(*factotum)
	f, err = NewFromDir(filepath.Join("testdata", "comment"))
	if err != nil {
		t.Errorf("NewFromDir(testdata/comment): %v", err)
	}
	fi2 := f.(*factotum)
	d1 := fi1.keys[fi1.current].ecdsaKeyPair.D
	d2 := fi2.keys[fi2.current].ecdsaKeyPair.D
	if d1.Cmp(d2) != 0 {
		t.Errorf("NewFromDir: comment improperly affected key")
	}

}

func TestSign(t *testing.T) {
	fi, err := NewFromDir(filepath.Join("testdata", "ok"))
	if err != nil {
		t.Errorf("NewFromDir(testdata/ok): %v", err)
	}
	_, err = fi.Sign([]byte("this is too long a string for p256"))
	if err == nil {
		t.Errorf("factotum.Sing(longstring) should have failed")
	}
}
