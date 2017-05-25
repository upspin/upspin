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
		userName string
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
		{"me@here.com", "me", S, "here.com", ""},
		{"me@here.com.", "me", S, "here.com", ""},
		{"me+you@here.com", "me+you", "you", "here.com", ""},
		{"me@HERE.com", "me", S, "here.com", ""}, // Lower-case the domain.
		{"me.and.my.shadow@here.com", "me.and.my.shadow", S, "here.com", ""},
		{hugeOKName + "@foo.com", hugeOKName, S, "foo.com", ""}, // Maximum accepted length, 254.
		{hugeBadName + "@foo.com", U, S, D, "name too long"},
		{"@", U, S, D, "invalid operation: missing user name"},
		{"user@" + hugeOKDomain, "user", S, hugeOKDomain, ""}, // Maximum domain element length, 63.
		{"user@" + hugeBadDomain, U, S, D, "invalid domain name element"},
		{"user@a..com", U, S, D, "invalid domain name element"},
		{"a@bcom", U, S, D, "domain name must contain a period"},
		{"a@b@.com", U, S, D, "user name must contain one @ symbol"},
		{"a@%.com", U, S, D, "bad symbol in domain name"},
		{"@bbc.com", U, S, D, "missing user name"},
		{"abc.com@", U, S, D, "missing domain name"},
		{"a@b.co", "a", S, "b.co", ""},
		{"a@b.c", U, S, D, "invalid domain name"},
		{"me@here/.com", U, S, D, "bad symbol in domain name"},
		{"me@here.com..", U, S, D, "invalid domain name"}, // Two trailing dots.
		{"me+@here.com", U, S, D, "empty +suffix in user name"},
		{"me+a+b@here.com", U, S, D, "multiple +suffixes in user name"},
		{"me+/x@here.com", U, S, D, "bad symbol in +suffix"},
		// A good PRECIS case: canonicalize the accent. These two should be the same user, "ê@here.com".
		{"\u00ea@here.com", "\u00ea", S, "here.com", ""},  // Single code point.
		{"e\u0302@here.com", "\u00ea", S, "here.com", ""}, // Accent as a combining character.
		// Bad PRECIS cases.
		{"henry\u2163@here.com", U, S, D, "precis: disallowed rune"},
		{"!!@here.com", U, S, D, "invalid operation: user name contains only punctuation"},
		// Special wildcard case.
		{"*@here.com", "*", S, "here.com", ""}, // Single code point.
	}
	for _, test := range tests {
		u, s, d, err := Parse(upspin.UserName(test.userName))
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

func TestParseUser(t *testing.T) {
	type cases struct {
		userName string
		user     string
		suffix   string
		errStr   string
	}
	hugeOKName := strings.Repeat("a", 255-1)
	hugeBadName := strings.Repeat("b", 255)
	// Constants to make the test cases a little easier to read.
	const (
		U = ""
		S
	)
	var tests = []cases{
		{"", U, S, "invalid operation: missing user name"},
		{"a", "a", S, ""},
		{"me", "me", S, ""},
		{"me+you", "me+you", "you", ""},
		{"me", "me", S, ""}, // Lower-case the domain.
		{"me@here.com", U, S, "illegal character '@'"},
		{"me.and.my.shadow", "me.and.my.shadow", S, ""},
		{hugeOKName, hugeOKName, S, ""}, // Maximum accepted length, 254.
		{hugeBadName, U, S, "name too long"},
		{"me+", U, S, "empty +suffix in user name"},
		{"me+a+b", U, S, "multiple +suffixes in user name"},
		{"me+/x", U, S, "bad symbol in +suffix"},
		// A good PRECIS case: canonicalize the accent. These two should be the same user, "ê".
		{"\u00ea", "\u00ea", S, ""},  // Single code point.
		{"e\u0302", "\u00ea", S, ""}, // Accent as a combining character.
		// Bad PRECIS cases.
		{"henry\u2163", U, S, "precis: disallowed rune"},
		{"!!", U, S, "invalid operation: user name contains only punctuation"},
		// Special wildcard case not valid here, only in Parse.
		{"*", "*", S, "user name contains only punctuation"},
	}
	for _, test := range tests {
		u, s, err := ParseUser(test.userName)
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

func TestParseDomain(t *testing.T) {
	type cases struct {
		domainName string
		domain     string
		errStr     string
	}
	hugeOKDomain := strings.Repeat("x", 63) + ".com"
	hugeBadDomain := strings.Repeat("y", 64) + ".com"
	// Constants to make the test cases a little easier to read.
	const D = ""
	var tests = []cases{
		{"", D, "invalid operation: missing domain name"},
		{"here.com", "here.com", ""},
		{"HERE.com", "here.com", ""},                    // Lower-case the domain.
		{"me@here.com", D, "bad symbol in domain name"}, // No @s in domain name.
		{hugeOKDomain, hugeOKDomain, ""},                // Maximum domain element length, 63.
		{hugeBadDomain, D, "invalid domain name element"},
		{"a..com", D, "invalid domain name element"},
		{"bcom", D, "domain name must contain a period"},
		{"b@.com", D, "bad symbol in domain name"},
		{".com", D, "invalid domain name element"},
		{"%.com", D, "bad symbol in domain name"},
		{"b.co", "b.co", ""},
		{"b.c", D, "invalid domain name"},
		{"here/.com", D, "bad symbol in domain name"},
		{"here.com..", D, "invalid domain name"}, // Two trailing dots.
		{"*", D, "domain name must contain a period"},
	}
	for _, test := range tests {
		d, err := ParseDomain(test.domainName)
		if test.errStr == "" {
			if err != nil {
				t.Errorf("%q: expected no error, got %q", test.domainName, err)
				continue
			}
			if d != test.domain {
				t.Errorf("%q: expected domain %q, got %q", test.domainName, test.domain, d)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q: expected %q error, got none", test.domainName, test.errStr)
			continue
		}
		if !strings.Contains(err.Error(), test.errStr) {
			t.Errorf("%q: expected %q, got %q", test.domainName, test.errStr, err)
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
