// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package user provides tools for parsing and validating user names.
package user // import "upspin.io/user"

import (
	"strings"

	"golang.org/x/text/secure/precis"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// Parse splits an upspin.UserName into user and domain and returns the pair.
// It also returns the "+" suffix part of the user name, if it has one. For example,
// given the user name
//	ann+backup@example.com
// it would return the strings
// 	"ann+backup" "backup" "example.com"
//
// Parsed validates the name as an e-mail address and lower-cases the  domain
// so it is canonical.
//
// The rules are:
//
// 	<name> := <user name>@<domain name>
//
// 	<domain name> :=
//
// 	- each . separated token < 64 characters
// 	- character set for tokens [a-z0-9\-]
// 	- final token at least two characters
// 	- whole name < 254 characters
// 	- characters are case insensitive
// 	- final period is OK, but we remove it
//
// We ignore the rules of punycode, which is defined in https://tools.ietf.org/html/rfc3490 .
//
// 	<user name> :=
//
// Names are validated and canonicalized by the UsernameCasePreserved profile
// of the RFC 7613, "Preparation, Enforcement, and Comparison of Internationalized Strings",
// also known as PRECIS.
//
// Further restrictions are added here. The only ASCII punctuation characters
// that are legal are "!#$%&'*+-/=?^_{|}~", and a name that is only ASCII punctuation
// is rejected.
//
// As a special case for use in Access and Group files, the name "*" is allowed.
//
// Case is significant and spaces are not allowed.
//
// The username suffix is tightly constrained: It uses the same character
// set as domains, but of course the spacing of periods is irrelevant.
//
// Facebook and Google constrain usernames to [a-zA-Z0-9+-.],
// ignoring the period and, in Google only, ignoring everything
// from a plus sign onwards. We accept a superset of this but do not
// follow the "ignore" rules.
//
func Parse(userName upspin.UserName) (user, suffix, domain string, err error) {
	const op = errors.Op("user.Parse")
	name := string(userName)
	if len(userName) >= 254 {
		return "", "", "", errors.E(op, errors.Invalid, userName, "name too long")
	}
	if strings.Count(name, "@") != 1 {
		return "", "", "", errors.E(op, errors.Invalid, userName, errors.Str("user name must contain one @ symbol"))
	}
	at := strings.IndexByte(name, '@')
	user, domain = name[:at], name[at+1:]
	if user == "*" {
		// An important special case:
	} else {
		user, suffix, err = parseUser(op, userName, user)
		if err != nil {
			return "", "", "", err
		}
	}
	domain, err = parseDomain(op, userName, domain)
	if err != nil {
		return "", "", "", err
	}
	return user, suffix, domain, nil
}

// ParseUser parses the component of a user name before the '@', that is, the
// user component of an email address. The rules are defined in the
// documentation for Parse except that "*" is not a valid user and the user name
// itself must be less than 255 bytes long.
func ParseUser(user string) (userName, suffix string, err error) {
	return parseUser(errors.Op("user.ParseUser"), upspin.UserName(user), user)
}

// parseUser is the implementation of ParseUser, also called by Parse.
// It takes the full UserName as well as the user component, to aid in error reporting.
func parseUser(op errors.Op, userName upspin.UserName, user string) (string, string, error) {
	if len(user) >= 255 {
		return errParseUser(op, userName, "user name too long")
	}
	if user == "" {
		return errParseUser(op, userName, "missing user name")
	}
	plus := strings.IndexByte(user, '+')
	if plus == len(user)-1 { // Check first because PRECIS dislikes + at end of string.
		return errParseUser(op, userName, "empty +suffix in user name")
	}
	// Validate and canonicalize the user name - and maybe suffix, but
	// the suffix is checked more thoroughly below. We include the suffix
	// here because PRECIS will prevent things like "+" or "ann+" or
	// "+ann" as the full name. That is, we do PRECIS validation on
	// the full user+suffix.
	user, err := canonicalize(user)
	if err != nil {
		return "", "", errors.E(op, errors.Invalid, user, err)
	}
	// Valid +suffix (if any)?
	suffix := ""
	if plus >= 0 {
		if plus == 0 {
			return errParseUser(op, userName, "user name cannot start with +suffix")
		}
		suffix = user[plus+1:]
		if strings.IndexByte(suffix, '+') > 0 {
			return errParseUser(op, userName, "multiple +suffixes in user name")
		}
		for _, c := range suffix {
			if !okDomainChar(c) {
				return errParseUser(op, userName, "bad symbol in +suffix")
			}
		}
	}
	return user, suffix, nil
}

// ParseDomain parses the component of a user name after the '@', that is, the
// domain component of an email address. The rules are defined in the
// documentation for Parse except the domain name itself must be less than 255
// bytes long.
func ParseDomain(domain string) (string, error) {
	return parseDomain(errors.Op("user.ParseDomain"), upspin.UserName(domain), domain)
}

// parseDomain is the implementation of ParseDomain, also called by Parse.
// It takes the full UserName as well as the domain component, to aid in error reporting.
func parseDomain(op errors.Op, userName upspin.UserName, domain string) (string, error) {
	if len(domain) >= 255 {
		return errParseDomain(op, userName, "domain name too long")
	}
	// Final period in domain is legal but is dropped.
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return errParseDomain(op, userName, "missing domain name")
	}
	if strings.Count(domain, ".") == 0 {
		return errParseDomain(op, userName, "domain name must contain a period")
	}
	// Valid domain name?
	period := -1 // First time through loop will fail if first byte is a period.
	isUpper := false
	for i, c := range domain {
		if !okDomainChar(c) {
			return errParseDomain(op, userName, "bad symbol in domain name")
		}
		if c == '.' {
			if i-1 >= period+64 {
				return errParseDomain(op, userName, "invalid domain name element")
			}
			if i-1 == period || i-1 >= period+64 {
				return errParseDomain(op, userName, "invalid domain name element")
			}
			period = i
		}
		if 'A' <= c && c <= 'Z' {
			isUpper = true
		}
	}
	// Last domain element must be at least two bytes  (".co")
	if period+2 >= len(domain) {
		return errParseDomain(op, userName, "invalid domain name")
	}
	// Lower-case the domain name if necessary.
	if isUpper {
		domain = strings.ToLower(domain)
	}
	return domain, nil
}

func errParseUser(op errors.Op, userName upspin.UserName, msg string) (u, s string, err error) {
	return "", "", errors.E(op, errors.Invalid, userName, msg)
}

func errParseDomain(op errors.Op, userName upspin.UserName, msg string) (d string, err error) {
	return "", errors.E(op, errors.Invalid, userName, msg)
}

func canonicalize(user string) (string, error) {
	// PRECIS allows any ASCII character, but we are more restrictive.
	// That's OK because the ASCII check is cheap and almost always
	// sufficient.
	allPunct := true
	simple := true
	for _, r := range user {
		if illegalASCIIPunctuation(r) {
			return "", errors.Errorf("illegal character %q", r)
		}
		if !legalASCIIPunctuation(r) {
			allPunct = false
		}
		if !simpleUserNameChar(r) {
			simple = false
		}
	}
	if allPunct {
		return "", errors.Errorf("user name contains only punctuation")
	}
	if !simple {
		return precis.UsernameCasePreserved.String(user)
	}
	return user, nil
}

// Used by canonicalize to identify simple strings that don't need PRECIS processing.
// Note we don't check punctuation here because identifiers allow punctuation but
// only in certain places; let PRECIS do the work. "*" is the exception.
func simpleUserNameChar(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	case '0' <= r && r <= '9':
		return true
	}
	return false
}

// illegalASCIIPunctuation reports whether the rune is an ASCII punctuation
// character that is allowed by PRECIS but not by us within a user name.
// We include @ because this does not look at the domain name, just the user part.
func illegalASCIIPunctuation(r rune) bool {
	return strings.ContainsRune(" @\"(),:;<>[\\]`", r)
}

// legalASCIIPunctuation reports whether the rune is an ASCII punctuation
// character that is allowed by us.
func legalASCIIPunctuation(r rune) bool {
	return strings.ContainsRune("!#.$%&'*+-/=?^_{|}~", r)
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

// Clean returns the user name in canonical form as described by
// the comments for the Parse function.
func Clean(userName upspin.UserName) (upspin.UserName, error) {
	user, _, domain, err := Parse(userName)
	if err != nil {
		return "", err
	}
	// Do we need to rebuild? Avoid allocation if we can.
	userString := string(userName)
	atSign := strings.IndexByte(userString, '@')
	if user == userString[:atSign] && domain == userString[atSign+1:] {
		return userName, nil
	}
	return upspin.UserName(user + "@" + domain), nil
}
