// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// This file deals with receiving signup user requests.

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"bufio"
	"bytes"
	"fmt"

	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
)

//var email mail.Mail

// Fields present in incoming emails via the /incoming handler.
const (
	dkim   = "dkim"
	from   = "from"
	spf    = "SPF"
	text   = "text"
	noHTML = ""

	emailTemplate = "I'm "
)

func incomingHandler(w http.ResponseWriter, r *http.Request) {
	user, pwd, ok := r.BasicAuth()
	if !ok {
		// Send a 200 OK so SendGrid does not retry.
		w.WriteHeader(http.StatusOK)
		log.Error.Printf("No authentication provided")
		return
	}
	defer r.Body.Close()
	if pwd != "mypasswd" || user != "upspin-sendgrid" {
		w.WriteHeader(http.StatusForbidden)
		log.Error.Printf("Error in basic auth: got user %q, pwd %q", user, pwd)
		return
	}
	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		log.Error.Printf("Error reading body: %s", err)
		return
	}

	log.Printf("Received something on /incoming: %s", data)
	w.WriteHeader(http.StatusOK)
}

func sendReply(to, subject, contents string) error {
	/*
		err = email.Send(to, serverName, subject, contents, noHTML)
		if err != nil {
			return err
		}*/

}

// parseMail parses the contents and headers of an email message, returning
// the from address and whether all verification steps passed. If the from
// address is not empty, it's okay to send an email back to sender with the
// error description, if any.
func parseMail(data []byte) (string, error) {
	type jsonEntries map[string]interface{}

	var je jsonEntries
	err := json.Unmarshal(data, &je)
	if err != nil {
		return "", errors.E(errors.IO, err)
	}

	from, found := je[from]
	if !found {
		return "", errors.E(errors.Invalid, errors.Str("no from field in email header"))
	}
	_, _, domain, err := user.Parse(upspin.UserName(from))
	if err != nil {
		return "", err
	}
	dkim, found := je[dkim]
	if !found {
		return from, errors.E(errors.IO, errors.Str("no DKIM information present"))
	}
	var dkimEntries jsonEntries
	err = json.Unmarshal(dkim, &dkimEntries)
	if err != nil {
		return from, errors.E(errors.IO, errors.Str("cannot parse DKIM information"))
	}
	// TODO: we could enumerate a few other domains we trust, such as gmail.com.
	passed, found := dkimEntries["@"+domain]
	if !found {
		return from, errors.E(errors.Permission, errors.Errorf("DKIM not present for domain %q", domain))
	}
	if passed != "pass" {
		return from, errors.E(errors.Permission, errors.Errorf("DKIM failed for domain %q", domain))
	}
	return from, nil
}

// States in the email parsing state machine.
// Refer to this template for what to look at:
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
	keyPreamble,
	key,
	signature
)

// parseBody parses the contents of the email and validates the public key and
// the signature, returning a User struct.
func parseBody(data []byte) (*upspin.User, error) {
	s := bufio.NewScanner(bytes.NewReader(data))
	state := email
	keyLine := 0
	var userName upspin.UserName
	var pubKey string
	var signature string
	for s.Scan() {
		var value string
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
				return errors.E(errors.Invalid, errors.Str("missing public key preamble"))
			}
			state = key
		case key:
			pubKey = pubKey + s.Text() + "\n"
			keyLine++
			if keyLine == 3 {
				state = signature
			}
		case signature:
			n, err := fmt.Sscanf(s.Text(), "Signature: %s", &signature)
			if n != 1 || err != nil {
				return errors.E(errors.Invalid, errors.Str("missing signature"))
			}
			break
		}
	}
}
