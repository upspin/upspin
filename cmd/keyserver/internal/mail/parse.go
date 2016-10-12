// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mail provides email parsing utilities for keyserver.
package mail

import (
	"bufio"
	"fmt"
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
	// gmail.com.
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

	// Get the email body, in text format.
	// TODO: maybe parse HTML if text is empty?
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
   Signature: 123453534534-32423423423
*/
const (
	email = iota
	keyPreamble
	key
	signature
	done
)

// ParseBody parses the contents of the email and returns the user name,
// public key and signature of the contents. No validation is performed other
// than the format of the email.
func ParseBody(data string) (upspin.UserName, upspin.PublicKey, upspin.Signature, error) {
	const op = "cmd/keyserver/internal/mail.ParseBody"
	s := bufio.NewScanner(strings.NewReader(data))
	var (
		state    = email
		keyLine  = 0
		userName upspin.UserName
		pubKey   string
		sig      upspin.Signature
	)
Outer:
	for s.Scan() {
		switch state {
		case email:
			var value string
			n, _ := fmt.Sscanf(s.Text(), "I am %s", &value)
			if n != 1 {
				// Keep going until we find where it begins.
				continue
			}
			userName = upspin.UserName(value)
			state = keyPreamble
		case keyPreamble:
			if s.Text() != "My public key is" {
				return "", "", sig, errors.E(op, errors.Invalid, errors.Str("missing public key preamble"))
			}
			state = key
		case key:
			pubKey = pubKey + s.Text()
			if keyLine < 2 {
				pubKey += "\n"
			}
			keyLine++
			if keyLine == 3 {
				state = signature
			}
		case signature:
			var sigStr string
			n, err := fmt.Sscanf(s.Text(), "Signature: %s", &sigStr)
			if n != 1 || err != nil {
				return "", "", sig, errors.E(op, errors.Invalid, errors.Str("missing signature"))
			}
			sigFields := strings.Split(sigStr, "-")
			if len(sigFields) != 2 {
				return "", "", sig, errors.E(errors.Invalid, errors.Errorf("invalid signature format: %q", sigStr))
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
			state = done
			break Outer
		}
	}
	if s.Err() != nil {
		return "", "", sig, errors.E(op, s.Err())
	}
	if state != done {
		return "", "", sig, errors.E(op, errors.Invalid, errors.Str("incomplete message body"))
	}
	return userName, upspin.PublicKey(pubKey), sig, nil
}
