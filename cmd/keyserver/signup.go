// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// This file deals with receiving signup user requests.

// TODOs:
// - make the body of the reply email much better: write better errors, send
//   links to a FAQ and use proper greeting and goodbye.
//

import (
	"crypto/ecdsa"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
	"upspin.io/cmd/keyserver/internal"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
)

const noHTML = ""

// Temporary flags for configuring an email provider.
// TODO: generalize these flags for more email providers and pass them
// via GCP metadata instead of flags.
var (
	emailApiKey   = flag.String("email_apikey", "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", "Email provider API auth token")
	emailUserName = flag.String("email_user", "sg-upspin", "Username that incoming email provider uses to authenticate with the keyserver")
	emailPassword = flag.String("email_pwd", "sg-upspin-1234", "Password that incoming email provider uses to authenticate with the keyserver")

	// email is the outbound email API.
	email mail.Mail
)

func incomingHandler(w http.ResponseWriter, r *http.Request) {
	caller, pwd, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusForbidden)
		log.Error.Printf("No authentication provided")
		return
	}
	if pwd != *emailPassword || caller != *emailUserName {
		// Maybe we changed password recently. Let the provider retry.
		w.WriteHeader(http.StatusForbidden)
		log.Error.Printf("Error in basic auth: got user %q, pwd %q", caller, pwd)
		return
	}

	// From here on, we accepted the incoming email.
	w.WriteHeader(http.StatusOK)

	// Now begin parsing it.
	err := r.ParseMultipartForm(1024 * 1024) // 1MB budget for email parsing.
	if err != nil {
		log.Error.Printf("Error parsing request: %v", err)
		return
	}

	from, body, err := internal.ParseMail(r.Form)
	if err != nil {
		// Error validating email. Maybe 'from' is known but without
		// a proper DKIM verification we can't reply to the sender since
		// it could be spoofed. Log and drop it on the floor.
		log.Printf("Received email from %q: %s", from, err)
		return
	}
	// From address is valid. Further errors can be replied by email.

	// Parse the contents of the email
	userName, pubKey, sig, err := internal.ParseBody(body)
	if err != nil {
		sendMailOrLog(from, "Error signing up for Upspin", err.Error())
		return
	}

	// Perform the following tasks next:
	// 1) signature validation
	// 2) compare userName with from address. Must be identical except for
	//    an optional suffix.
	// 3) verify userName does not yet exist.
	if err := validateSignature(userName, pubKey, sig); err != nil {
		sendMailOrLog(from, "Error validating signature", err.Error())
	}
	if string(userName) != from {
		// Maybe they don't match because of a suffix.
		// Test that hypothesis here.
		u, _, domain, err := user.Parse(userName)
		if err != nil {
			sendMailOrLog(from, "Error validating email address", err.Error())
			return
		}
		if from != fmt.Sprintf("%s@%s", u, domain) {
			sendMailOrLog(from, "Error validating email address",
				fmt.Sprintf("email was sent from %q, but contents mention %q.", from, userName))
			return
		}
		// Otherwise, it's okay. The user owns the suffixed user name.
	}

	// Lookup userName. It must not exist yet.
	_, err = key.Lookup(userName)
	if err == nil || !errors.Match(errors.E(errors.NotExist), err) {
		// TODO: this leaks the fact that the user exists, even though we're not saying it.
		sendMailOrLog(from, "Can't create user", fmt.Sprintf("Can't create user for %s at this time", userName))
		// Also log the error, if any.
		if err != nil {
			log.Error.Printf("Error looking up %q: %s", userName, err)
		}
		return
	}

	// TODO: create the user, finally.
	// (leaving as a TODO for another CL while we test this one).
	sendMailOrLog(from, "Welcome to Upspin",
		fmt.Sprintf("Your account %q with public key\n%s\nwould have been created.", userName, pubKey))
}

func validateSignature(u upspin.UserName, k upspin.PublicKey, sig string) error {
	// Parse signature
	sigs := strings.Split(sig, "-")
	if len(sigs) != 2 {
		return errors.E(errors.Invalid, errors.Errorf("invalid signature format: %q", sig))
	}
	var rs, ss big.Int
	if _, ok := rs.SetString(sig[0], 10); !ok {
		return errors.E(errors.Invalid, errors.Errorf("invalid signature fragment: %q", sig[0]))
	}
	if _, ok := ss.SetString(sig[1], 10); !ok {
		return errors.E(errors.Invalid, errors.Errorf("invalid signature fragment: %q", sig[1]))
	}
	// Validate signature.
	hash := []byte(u + k)
	ecdsaPubKey, _, err := factotum.ParsePublicKey(k)
	if err != nil {
		return err
	}
	if !ecdsa.Verify(ecdsaPubKey, hash, &rs, &ss) {
		return errors.E(errors.Invalid, errors.Str("signature does not match"))
	}
	return nil
}

// sendMailOrLog sends email to a recipient with a given subject and contents.
// If email processing fails, it logs an error.
func sendMailOrLog(to, subject, contents string) {
	if email == nil {
		initEmail()
	}
	err := email.Send(to, serverName, subject, contents, noHTML)
	if err != nil {
		log.Error.Printf("Error sending email: %s", err)
	}
}

var emailLock sync.Mutex

func initEmail() {
	emailLock.Lock()
	if email == nil {
		email = sendgrid.New(*emailApiKey, "upspin.io")
	}
	emailLock.Unlock()
}
