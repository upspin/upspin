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

// SignupMessage represents the contents of a user signup message.
type SignupMessage struct {
	UserName  upspin.UserName
	PublicKey upspin.PublicKey
	Dir       upspin.Endpoint
	Store     upspin.Endpoint
	Signature upspin.Signature
}

// ParseBody parses the signup email and returns its contents.
// Only the email address and endpoints are (superficially) validated.
func ParseBody(data string) (*SignupMessage, error) {
	const op = "cmd/keyserver/internal/mail.ParseBody"

	const (
		userPrefix    = "I am "
		keyPreamble   = "My public key is:"
		dirPreamble   = "My directory server is:"
		storePreamble = "My store server is:"
		sigPreamble   = "Signature:"
	)

	clean := strings.TrimSpace(data)
	clean = strings.Replace(clean, ";", "\n", -1)
	for _, p := range []string{keyPreamble, dirPreamble, storePreamble, sigPreamble} {
		clean = strings.Replace(clean, p, p+"\n", 1)
	}
	clean = strings.TrimSpace(clean)

	// HTML markers are converted to a * by Gmail.
	// Yahoo strips all HTML and removes new lines.
	// TODO: figure out what others do with HTML.
	clean = strings.Replace(clean, "*", "", -1)

	lines := strings.Split(clean, "\n")
	if len(lines) < 7 {
		return nil, errors.E(op, errors.Invalid, errors.Str("badly formatted email message"))
	}
	i := 0
	var line string
	next := func() bool {
		for i < len(lines) {
			line = strings.TrimSpace(lines[i])
			i++
			if line != "" {
				return true
			}
		}
		return false
	}
	advanceTo := func(prefix string) bool {
		for next() {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		return false
	}

	if !advanceTo(userPrefix) {
		return nil, errors.E(op, errors.Invalid, errors.Str("missing username"))
	}
	userName := upspin.UserName(line[len(userPrefix):])

	// Key
	if !advanceTo(keyPreamble) {
		return nil, errors.E(op, errors.Invalid, errors.Str("missing key preamble line"))
	}
	var keyStr string
	keyLines := 0
	for keyLines < 3 && next() {
		keyStr += line + "\n"
		keyLines++
	}
	if keyLines != 3 {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid public key format: %q", keyStr))
	}
	pubKey := upspin.PublicKey(keyStr)

	// Dir endpoint
	if !advanceTo(dirPreamble) || !next() {
		return nil, errors.E(op, errors.Invalid, errors.Str("missing directory endpoint"))
	}
	dirEndpoint, err := upspin.ParseEndpoint(line)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Store endpoint
	if !advanceTo(storePreamble) || !next() {
		return nil, errors.E(op, errors.Invalid, errors.Str("missing store endpoint"))
	}
	storeEndpoint, err := upspin.ParseEndpoint(line)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// Signature
	if !advanceTo(sigPreamble) || !next() {
		return nil, errors.E(op, errors.Invalid, errors.Str("missing signature"))
	}
	// TODO: parsing signature should move to package upspin. Signature
	// should also not have pointers, but big.Ints directly.
	var rs, ss big.Int
	if _, ok := rs.SetString(line, 10); !ok {
		return nil, errors.E(errors.Invalid, errors.Errorf("invalid signature fragment: %q", line))
	}
	if !next() {
		return nil, errors.E(op, errors.Invalid, errors.Str("incomplete signature"))
	}
	if _, ok := ss.SetString(line, 10); !ok {
		return nil, errors.E(errors.Invalid, errors.Errorf("invalid signature fragment: %q", line))
	}

	return &SignupMessage{
		UserName:  userName,
		PublicKey: pubKey,
		Dir:       *dirEndpoint,
		Store:     *storeEndpoint,
		Signature: upspin.Signature{R: &rs, S: &ss},
	}, nil
}
