// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package user

import (
	"strings"
	"testing"

	"upspin.io/upspin"
)

func TestUserAndDomain(t *testing.T) {
	type cases struct {
		userName upspin.UserName
		user     string
		domain   string
		errStr   string
	}
	hugeOKName := strings.Repeat("a", 254-9)
	hugeBadName := strings.Repeat("b", 254-8)
	hugeOKDomain := strings.Repeat("x", 63) + ".com"
	hugeBadDomain := strings.Repeat("y", 64) + ".com"
	var tests = []cases{
		{upspin.UserName("me@here.com"), "me", "here.com", ""},
		{upspin.UserName("me@HERE.com"), "me", "here.com", ""},                // Lower-case the domain.
		{upspin.UserName(hugeOKName + "@foo.com"), hugeOKName, "foo.com", ""}, // Maximum accepted length, 254.
		{upspin.UserName(hugeBadName + "@foo.com"), "", "", "name too long"},
		{upspin.UserName("@"), "", "", "syntax error: missing user name"},
		{upspin.UserName("user@" + hugeOKDomain), "user", hugeOKDomain, ""}, // Maximum domain element length, 63.
		{upspin.UserName("user@" + hugeBadDomain), "", "", "invalid domain name element"},
		{upspin.UserName("user@a..com"), "", "", "invalid domain name element"},
		{upspin.UserName("a@bcom"), "", "", "domain name must contain a period"},
		{upspin.UserName("a@b@.com"), "", "", "user name must contain one @ symbol"},
		{upspin.UserName("a@%.com"), "", "", "bad symbol in domain name"},
		{upspin.UserName("@bbc.com"), "", "", "missing user name"},
		{upspin.UserName("abc.com@"), "", "", "missing domain name"},
		{upspin.UserName("a@b.co"), "a", "b.co", ""},
		{upspin.UserName("a@b.c"), "", "", "invalid domain name"},
		{upspin.UserName("me@here/.com"), "", "", "bad symbol in domain name"},
		{upspin.UserName("me@here.com."), "me", "here.com.", "invalid domain name"}, // We disallow the trailing dot.
		{upspin.UserName("me@here.com.."), "", "", "invalid domain name"},
	}
	for _, test := range tests {
		u, d, err := Parse(test.userName)
		if test.errStr == "" {
			if err != nil {
				t.Errorf("%q: Expected no error, got %q", test.userName, err)
				continue
			}
			if u != test.user {
				t.Errorf("%q: expected user %q, got %q", test.userName, test.user, u)
			}
			if d != test.domain {
				t.Errorf("%q: expected domain %q, got %q", test.userName, test.domain, u)
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
