// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// This file deals with receiving signup user requests.

import (
	"flag"
	"net/http"
	"sync"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
	"upspin.io/cmd/keyserver/internal"
	"upspin.io/log"
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
	err := r.ParseMultipartForm(1024 * 1024) // 1MB budget for email parsing.
	if err != nil {
		// Send a 200 OK so the email provider does not retry.
		w.WriteHeader(http.StatusOK)
		log.Error.Printf("Error parsing request: %v", err)
		return
	}
	user, pwd, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusOK)
		log.Error.Printf("No authentication provided")
		return
	}
	if pwd != *emailPassword || user != *emailUserName {
		// Maybe we changed password recently. Let the provider retry.
		w.WriteHeader(http.StatusForbidden)
		log.Error.Printf("Error in basic auth: got user %q, pwd %q", user, pwd)
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
		// TODO: make the body of the email much better. Send pointers
		// to a FAQ and use proper greeting and goodbye.
		err = sendReply(from, "Error signing up for Upspin", err.Error())
		if err != nil {
			log.Error.Printf("Error sending email: %s", err)
		}
		return
	}

	// TODO: perform the following tasks next:
	// 1) signature validation
	// 2) compare userName with from address. Must be identical except for
	//    an optional suffix.
	// 3) verify userName does not yet exist.

	log.Printf("Received email signup from %q with pubKey %q, signature %q", userName, pubKey, sig)
	w.WriteHeader(http.StatusOK)
}

func sendReply(to, subject, contents string) error {
	if email == nil {
		initEmail()
	}
	err := email.Send(to, serverName, subject, contents, noHTML)
	if err != nil {
		return err
	}
	return nil
}

var emailLock sync.Mutex

func initEmail() {
	emailLock.Lock()
	if email == nil {
		email = sendgrid.New(*emailApiKey, "upspin.io")
	}
	emailLock.Unlock()
}
