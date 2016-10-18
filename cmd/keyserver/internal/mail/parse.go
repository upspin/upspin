// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mail provides email parsing utilities for keyserver.
package mail

import (
	"math/big"
	goMail "net/mail"
	"net/url"
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/user"
)

// Fields possibly present in email headers.
const (
	hdrDKIM = "dkim"
	hdrFrom = "from"
	hdrSPF  = "SPF"

	pass = "pass"
)

// ParseMail parses the headers and body of an email message, returning the from
// address and the email text body. ParseMail is only successful if the from
// address passes DKIM and SPF checks. No validation is done on the email body.
func ParseMail(data url.Values) (from string, body string, err error) {
	const op = "cmd/keyserver/internal/mail.ParseMail"
	from = data.Get(hdrFrom)
	if from == "" {
		return "", "", errors.E(op, errors.Invalid, errors.Str("no From: field in email header"))
	}
	addr, err := goMail.ParseAddress(from)
	if err != nil {
		return "", "", errors.E(op, errors.Invalid, err)
	}
	from = addr.Address

	_, _, domain, err := user.Parse(upspin.UserName(from))
	if err != nil {
		return "", "", errors.E(op, err)
	}
	dkim := data.Get(hdrDKIM)
	if dkim == "" {
		return "", "", errors.E(op, errors.IO, errors.Str("no DKIM information present"))
	}
	dkimDomain, dkimStatus, err := parseDKIM(dkim)
	if err != nil {
		return "", "", errors.E(op, err)
	}
	// TODO: we could allow a whitelist of a few domains we trust, such as
	// gmail.com, which we may trust with account delegation to other domains.
	if dkimDomain != domain {
		return "", "", errors.E(op, errors.Permission, errors.Errorf("DKIM not present for domain %q", domain))
	}
	if dkimStatus != pass {
		return "", "", errors.E(op, errors.Permission, errors.Errorf("DKIM failed for domain %q", domain))
	}
	spf := data.Get(hdrSPF)
	if spf == "" {
		return "", "", errors.E(op, errors.Permission, errors.Str("SPF information not present"))
	}
	if spf != pass {
		return "", "", errors.E(op, errors.Permission, errors.Str("SPF failed"))
	}

	// Get the email body, in text format (always present, even when
	// original is in HTML).
	body = data.Get("text")
	if body == "" {
		return "", "", errors.E(op, errors.Invalid, errors.Str("empty email body"))
	}
	return
}

func parseDKIM(s string) (domain, status string, err error) {
	s = strings.Trim(s, "{}")
	fields := strings.Split(s, ":")
	if len(fields) != 2 {
		return "", "", errors.E(errors.Internal, errors.Errorf("DKIM information not parseable"))
	}
	return strings.TrimSpace(fields[0][1:]), strings.TrimSpace(fields[1]), nil
}

// States in the email parsing state machine.
// Refer to this template to understand the states:
/*
   I am foo@bar.com
   My public key is
   p256
   1063349993423423435345345345345345340
   3453453457828271720003453453245354698
   Signature:
   123453534534:32423423423
*/

// ParseBody parses the contents of the email and returns the user name,
// public key and signature of the contents. No validation is performed other
// than the format of the email.
func ParseBody(data string) (upspin.UserName, upspin.PublicKey, upspin.Signature, error) {
	const op = "cmd/keyserver/internal/mail.ParseBody"
	var sig upspin.Signature

	clean := strings.TrimSpace(data)
	// HTML markers are converted to a * by Gmail.
	// Yahoo strips all HTML and does not
	// replace it.
	// TODO: figure out what others do.
	clean = strings.Replace(clean, "*", "", -1)

	lines := strings.Split(clean, "\n")
	if len(lines) < 7 {
		return "", "", sig, errors.E(op, errors.Invalid, errors.Str("badly formatted email message"))
	}
	i := 0
	var line string
	next := func() bool {
		i++
		if i > len(lines) {
			return false
		}
		line = strings.TrimSpace(lines[i-1])
		return true
	}
	advanceTo := func(prefix string) bool {
		for next() {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		return false
	}

	const userPrefix = "I am "
	if !advanceTo(userPrefix) {
		return "", "", sig, errors.E(op, errors.Invalid, errors.Str("missing username"))
	}
	userName := upspin.UserName(line[len(userPrefix):])

	const keyPreamble = "My public key is"
	if !advanceTo(keyPreamble) {
		return "", "", sig, errors.E(op, errors.Invalid, errors.Str("missing key preamble line"))
	}

	// Key
	var keyStr string
	keyLines := 0
	for keyLines < 3 && next() {
		if line == "" {
			continue
		}
		keyStr += line + "\n"
		keyLines++
	}
	if keyLines != 3 {
		return "", "", sig, errors.E(op, errors.Invalid, errors.Errorf("invalid public key format: %q", keyStr))
	}
	pubKey := upspin.PublicKey(keyStr)

	const sigPreamble = "Signature:"
	if !advanceTo(sigPreamble) {
		return "", "", sig, errors.E(op, errors.Invalid, errors.Str("missing signature preamble line"))
	}

	if !next() {
		return "", "", sig, errors.E(errors.Invalid, errors.Str("missing signature"))
	}

	// TODO: parsing signature should move to package upspin. Signature
	// should also not have pointers, but big.Ints directly.
	sigFields := strings.Split(line, ":")
	if len(sigFields) != 2 {
		return "", "", sig, errors.E(errors.Invalid, errors.Errorf("invalid signature: %q", line))
	}
	var rs, ss big.Int
	if _, ok := rs.SetString(sigFields[0], 10); !ok {
		return "", "", sig, errors.E(errors.Invalid, errors.Errorf("invalid signature fragment: %q", sigFields[0]))
	}
	if _, ok := ss.SetString(sigFields[1], 10); !ok {
		return "", "", sig, errors.E(errors.Invalid, errors.Errorf("invalid signature fragment: %q", sigFields[1]))
	}
	sig.R = &rs
	sig.S = &ss

	return userName, pubKey, sig, nil
}
