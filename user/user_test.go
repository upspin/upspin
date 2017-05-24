// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package user

import (
	"fmt"
	"strings"
	"testing"

	"upspin.io/upspin"
)

func TestParse(t *testing.T) {
	type cases struct {
		userName upspin.UserName
		user     string
		suffix   string
		domain   string
		errStr   string
	}
	hugeOKName := strings.Repeat("a", 254-9)
	hugeBadName := strings.Repeat("b", 254-8)
	hugeOKDomain := strings.Repeat("x", 63) + ".com"
	hugeBadDomain := strings.Repeat("y", 64) + ".com"
	// Constants to make the test cases a little easier to read.
	const (
		U = ""
		S
		D
	)
	var tests = []cases{
		{upspin.UserName("me@here.com"), "me", S, "here.com", ""},
		{upspin.UserName("me@here.com."), "me", S, "here.com", ""},
		{upspin.UserName("me+you@here.com"), "me+you", "you", "here.com", ""},
		{upspin.UserName("me@HERE.com"), "me", S, "here.com", ""}, // Lower-case the domain.
		{upspin.UserName("me.and.my.shadow@here.com"), "me.and.my.shadow", S, "here.com", ""},
		{upspin.UserName(hugeOKName + "@foo.com"), hugeOKName, S, "foo.com", ""}, // Maximum accepted length, 254.
		{upspin.UserName(hugeBadName + "@foo.com"), U, S, D, "name too long"},
		{upspin.UserName("@"), U, S, D, "invalid operation: missing user name"},
		{upspin.UserName("user@" + hugeOKDomain), "user", S, hugeOKDomain, ""}, // Maximum domain element length, 63.
		{upspin.UserName("user@" + hugeBadDomain), U, S, D, "invalid domain name element"},
		{upspin.UserName("user@a..com"), U, S, D, "invalid domain name element"},
		{upspin.UserName("a@bcom"), U, S, D, "domain name must contain a period"},
		{upspin.UserName("a@b@.com"), U, S, D, "user name must contain one @ symbol"},
		{upspin.UserName("a@%.com"), U, S, D, "bad symbol in domain name"},
		{upspin.UserName("@bbc.com"), U, S, D, "missing user name"},
		{upspin.UserName("abc.com@"), U, S, D, "missing domain name"},
		{upspin.UserName("a@b.co"), "a", S, "b.co", ""},
		{upspin.UserName("a@b.c"), U, S, D, "invalid domain name"},
		{upspin.UserName("me@here/.com"), U, S, D, "bad symbol in domain name"},
		{upspin.UserName("me@here.com.."), U, S, D, "invalid domain name"}, // Two trailing dots.
		{upspin.UserName("me+@here.com"), U, S, D, "empty +suffix in user name"},
		{upspin.UserName("me+a+b@here.com"), U, S, D, "multiple +suffixes in user name"},
		{upspin.UserName("me+/x@here.com"), U, S, D, "bad symbol in +suffix"},
		// A good PRECIS case: canonicalize the accent. These two should be the same user, "ê@here.com".
		{upspin.UserName("\u00ea@here.com"), "\u00ea", S, "here.com", ""},  // Single code point.
		{upspin.UserName("e\u0302@here.com"), "\u00ea", S, "here.com", ""}, // Accent as a combining character.
		// Bad PRECIS cases.
		{upspin.UserName("henry\u2163@here.com"), U, S, D, "precis: disallowed rune"},
		{upspin.UserName("!!@here.com"), U, S, D, "invalid operation: user name contains only punctuation"},
		// Special wildcard case.
		{upspin.UserName("*@here.com"), "*", S, "here.com", ""}, // Single code point.
	}
	for _, test := range tests {
		u, s, d, err := Parse(test.userName)
		if test.errStr == "" {
			if err != nil {
				t.Errorf("%q: expected no error, got %q", test.userName, err)
				continue
			}
			if u != test.user {
				t.Errorf("%q: expected user %q, got %q", test.userName, test.user, u)
			}
			if s != test.suffix {
				t.Errorf("%q: expected suffix %q, got %q", test.userName, test.suffix, s)
			}
			if d != test.domain {
				t.Errorf("%q: expected domain %q, got %q", test.userName, test.domain, d)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: expected %q error, got none", test.userName, test.errStr)
			continue
		}
		if !strings.Contains(err.Error(), test.errStr) {
			t.Errorf("%q: expected %q, got %q", test.userName, test.errStr, err)
			continue
		}
	}
}

func TestASCII(t *testing.T) {
	for i := rune(0); i < 0x80; i++ {
		name := fmt.Sprintf("a%ca@here.com", i)
		_, _, _, err := Parse(upspin.UserName(name))
		ok := okASCII(i)
		switch {
		case ok && err == nil:
			// OK
		case !ok && err != nil:
			// OK
		case ok && err != nil:
			t.Errorf("%c should be good, is bad", i)
		case !ok && err == nil:
			t.Errorf("%c should be bad, is good", i)
		}
	}
}

func okASCII(r rune) bool {
	switch {
	case '0' <= r && r <= '9':
		return true
	case 'a' <= r && r <= 'z':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	}
	return legalASCIIPunctuation(r)
}

func TestClean(t *testing.T) {
	type cases struct {
		in  upspin.UserName
		out upspin.UserName
		ok  bool
	}
	tests := []cases{
		{"", "", false},
		{"abc@", "", false},
		{"abc@def.com", "abc@def.com", true},
		{"abc@DEF.com", "abc@def.com", true},          // lower-case the domain.
		{"e\u0302@here.com", "\u00ea@here.com", true}, // PRECIS canonicalization.
	}
	for _, test := range tests {
		out, err := Clean(test.in)
		if out == test.out && test.ok == (err == nil) {
			// All good.
			continue
		}
		if err != nil && !test.ok {
			// All good, caught the error.
			continue
		}
		t.Errorf("Clean(%q)=(%q, %v); expected %q with ok==%t", test.in, out, err, test.out, test.ok)
	}
}

func TestCleanNoAlloc(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		Clean("abc@def.com")
	})
	t.Log("allocs:", allocs)
	if allocs != 0 {
		t.Fatal("expected no allocations, got ", allocs)
	}
}
