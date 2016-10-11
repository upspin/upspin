// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package internal provides email parsing utilities for keyserver.
package internal

import (
	"bufio"
	"fmt"
	"net/url"
	"strings"

	"upspin.io/errors"
	"upspin.io/upspin"
	"upspin.io/user"
)

// Fields possibly present in email headers.
const (
	dkim = "dkim"
	from = "from"
	spf  = "SPF"
	text = "text"
	pass = "pass"
)

// ParseMail parses the headers and body of an email message, returning the from
// address and the email text body. ParseMail is only successful if the from
// address passes DKIM and SPF checks. No validation is done on the email body.
func ParseMail(data url.Values) (string, string, error) {
	from := data.Get(from)
	if from == "" {
		return "", "", errors.E(errors.Invalid, errors.Str("no from field in email header"))
	}
	// Perhaps from is encoded like this "Some dude <dude@gmail.com>".
	if idx := strings.Index(from, "<"); idx >= 0 {
		end := strings.Index(from, ">")
		if end < idx {
			return "", "", errors.E(errors.Invalid, errors.Str("invalid email format"))
		}
		from = from[idx+1 : end]
	}

	_, _, domain, err := user.Parse(upspin.UserName(from))
	if err != nil {
		return "", "", err
	}
	dkim := data.Get(dkim)
	if dkim == "" {
		return "", "", errors.E(errors.IO, errors.Str("no DKIM information present"))
	}
	dkim = strings.Trim(dkim, "{}")
	fields := strings.Split(dkim, ":")
	if len(fields) != 2 {
		return "", "", errors.E(errors.Internal, errors.Errorf("DKIM information not parseable"))
	}
	dkimDomain := strings.TrimSpace(fields[0][1:]) // remove spaces and leading "@"
	dkimStatus := strings.TrimSpace(fields[1])
	// TODO: we could allow a whitelist of a few domains we trust, such as
	// gmail.com.
	if dkimDomain != domain {
		return "", "", errors.E(errors.Permission, errors.Errorf("DKIM not present for domain %q", domain))
	}
	if dkimStatus != pass {
		return "", "", errors.E(errors.Permission, errors.Errorf("DKIM failed for domain %q", domain))
	}
	spf := data.Get(spf)
	if spf == "" {
		return "", "", errors.E(errors.Permission, errors.Str("SPF information not present"))
	}
	if spf != pass {
		return "", "", errors.E(errors.Permission, errors.Str("SPF failed"))
	}

	// Get the email body, in text format.
	// TODO: maybe parse HTML if text is empty?
	text := data.Get(text)
	if text == "" {
		return "", "", errors.E(errors.Invalid, errors.Str("empty email body"))
	}
	return from, text, nil
}

// States in the email parsing state machine.
// Refer to this template to understand the states:
/*
   I’m foo@bar.com
   My public key is
   p256
   1063349993423423435345345345345345340
   3453453457828271720003453453245354698
   Signature: ff2343ab34f43554d234e325ffac389000
*/
const (
	email = iota
	keyPreamble
	key
	signature
)

// ParseBody parses the contents of the email and returns the user name,
// public key and signature of the contents. No validation is performed other
// than the format of the email.
func ParseBody(data string) (upspin.UserName, upspin.PublicKey, string, error) {
	s := bufio.NewScanner(strings.NewReader(data))
	state := email
	keyLine := 0
	var userName upspin.UserName
	var pubKey string
	var sig string
Outer:
	for s.Scan() {
		switch state {
		case email:
			var value string
			n, _ := fmt.Sscanf(s.Text(), "I'm %s", &value)
			if n != 1 {
				// Keep going until we find where it begins.
				continue
			}
			userName = upspin.UserName(value)
			state = keyPreamble
		case keyPreamble:
			_, err := fmt.Sscanf(s.Text(), "My public key is")
			if err != nil {
				return "", "", "", errors.E(errors.Invalid, errors.Str("missing public key preamble"))
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
			n, err := fmt.Sscanf(s.Text(), "Signature: %s", &sig)
			if n != 1 || err != nil {
				return "", "", "", errors.E(errors.Invalid, errors.Str("missing signature"))
			}
			break Outer
		}
	}
	if s.Err() != nil {
		return "", "", "", s.Err()
	}
	return userName, upspin.PublicKey(pubKey), sig, nil
}
