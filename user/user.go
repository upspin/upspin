// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package user provides tools for parsing and validating user names.
package user

import (
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// Parse splits an upspin.UserName into user and domain and returns the pair.
// It also returns the "+" suffix part of the user name, if it has one. For example,
// given the user name
//	joe+backup@blow.com
// it would return the strings
// 	"joe+backup" "backup" "blow.com"
//
// Parsed validates the name as an e-mail address and lower-cases the  domain
// so it is canonical.
//
// TODO: Need to think about Unicode user names.
//
// The rules are:
//
// <name> := <user name>@<domain name>
//
// <domain name> :=
//
// - each . separated token < 64 characters
// - character set for tokens [a-z0-9\-]
// - final token at least two characters
// - whole name < 254 characters
// - characters are case insensitive
// - final period is OK.
//
// We ignore the rules of punycode, which is defined in https://tools.ietf.org/html/rfc3490 .
//
// <user name> :=
//
// Names are are constrained by what SMTP will allow, so these should be good:
//
// - upper and lower case A-Z, case matters.
// - 0-9  !#$%&'*+-/=?^_{|}~
// - utf // TODO.
// - spaces
//
// Spaces may be problematic for us but we allow them here. TODO?
//
// The username suffix is still more constrained: It uses the same character
// set as domains, but of course without the need for periods.
//
// Facebook and Google constrain you to [a-zA-Z0-9+-.],
// ignoring the period and, in Google only, ignoring everything
// from a plus sign onwards. We accept this set but do not follow
// the ignore rules.
//
func Parse(userName upspin.UserName) (user, suffix, domain string, err error) {
	name := string(userName)
	if len(userName) >= 254 {
		return errUserName(userName, "name too long")
	}
	if strings.Count(name, "@") != 1 {
		return errUserName(userName, "user name must contain one @ symbol")
	}
	at := strings.IndexByte(name, '@')
	user, domain = name[:at], name[at+1:]
	if user == "" {
		return errUserName(userName, "missing user name")
	}
	if domain == "" {
		return errUserName(userName, "missing domain name")
	}
	if strings.Count(domain, ".") == 0 {
		return errUserName(userName, "domain name must contain a period")
	}
	// Valid user name?
	for _, c := range user {
		if !okUserNameChar(c) {
			return errUserName(userName, "bad symbol in user name")
		}
	}
	// Valid +suffix (if any)?
	if plus := strings.IndexByte(user, '+'); plus >= 0 {
		if plus == 0 {
			return errUserName(userName, "user name cannot start with +suffix")
		}
		suffix = user[plus+1:]
		if suffix == "" {
			return errUserName(userName, "empty +suffix in user name")
		}
		if strings.IndexByte(suffix, '+') > 0 {
			return errUserName(userName, "multiple +suffixes in user name")
		}
		for _, c := range suffix {
			if !okDomainChar(c) {
				return errUserName(userName, "bad symbol in +suffix")
			}
		}
	}
	// Valid domain name?
	period := -1 // First time through loop will fail if first byte is a period.
	isUpper := false
	for i, c := range domain {
		if !okDomainChar(c) {
			return errUserName(userName, "bad symbol in domain name")
		}
		if c == '.' {
			if i-1 >= period+64 {
				return errUserName(userName, "invalid domain name element")
			}
			if i-1 == period || i-1 >= period+64 {
				return errUserName(userName, "invalid domain name element")
			}
			period = i
		}
		if 'A' <= c && c <= 'Z' {
			isUpper = true
		}
	}
	// Last domain element must be at least two bytes  (".co")
	if period+2 >= len(domain) {
		return errUserName(userName, "invalid domain name")
	}
	// Lower-case the domain name if necessary.
	if isUpper {
		domain = strings.ToLower(domain)
	}
	return user, suffix, domain, nil
}

func errUserName(user upspin.UserName, msg string) (u, s, d string, err error) {
	const op = "user.Parse"
	return "", "", "", errors.E(op, errors.Syntax, user, errors.Str(msg))
}

// See the comments for UserAndDomain.
func okUserNameChar(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	case '0' <= r && r <= '9':
		return true
	case strings.ContainsRune("!#$%&'*.+-/=?^_{|}~", r):
		return true
	}
	return false
}

// See the comments for UserAndDomain.
func okDomainChar(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	case '0' <= r && r <= '9':
		return true
	case strings.ContainsRune("+-.", r):
		return true
	}
	return false
}
