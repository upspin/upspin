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
	"net/http"
	"sync"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
	inbound "upspin.io/cmd/keyserver/internal/mail"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
)

const noHTML = ""

// Temporary flags for configuring an email provider.
// TODO(edpin, adg): move to the new deploy hotness.
var (
	emailAPIKey   = flag.String("email_apikey", "", "outbound email provider API auth token")
	emailUserName = flag.String("email_user", "", "username of incoming email provider to authenticate with the keyserver")
	emailPassword = flag.String("email_pwd", "", "password of incoming email provider to authenticate with the keyserver")

	// email is the outbound mail API.
	email mail.Mail
)

func mailHandler(w http.ResponseWriter, r *http.Request) {
	caller, pwd, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusForbidden)
		log.Error.Printf("No authentication provided")
		return
	}
	if pwd != *emailPassword || caller != *emailUserName {
		// Maybe we changed password recently. Let the provider retry.
		w.WriteHeader(http.StatusForbidden)
		log.Error.Printf("Error in basic auth: got user %q and hash-pwd %q", caller, sha256key.Of([]byte(pwd)).String())
		return
	}

	// From here on, we accepted the delivery of the incoming email. Now
	// parse its headers and contents.
	w.WriteHeader(http.StatusOK)

	// Now begin parsing it.
	err := r.ParseMultipartForm(1024 * 1024) // 1MB budget for email parsing.
	if err != nil {
		log.Error.Printf("Error parsing request: %v", err)
		return
	}

	from, body, err := inbound.ParseMail(r.Form)
	if err != nil {
		// Error validating email. Maybe 'from' is known but without
		// a proper DKIM verification we can't reply to the sender since
		// it could be spoofed. Log and drop it on the floor.
		log.Printf("Received email from %q: %s", from, err)
		return
	}
	// From address is valid. Further errors can be replied by email.

	// Parse the contents of the email
	userName, pubKey, sig, err := inbound.ParseBody(body)
	if err != nil {
		sendMail(from, "Error signing up for Upspin", err.Error())
		return
	}

	// Perform the following tasks next:
	// 1) signature validation
	// 2) compare userName with from address. Must be identical except for
	//    an optional suffix.
	// 3) verify userName does not yet exist.
	if err := validateSignature(userName, pubKey, sig); err != nil {
		sendMail(from, "Error validating signature", err.Error())
		return
	}
	if string(userName) != from {
		// Maybe they don't match because of a suffix.
		// Test that hypothesis here.
		u, _, domain, err := user.Parse(userName)
		if err != nil {
			sendMail(from, "Error validating email address", err.Error())
			return
		}
		if from != fmt.Sprintf("%s@%s", u, domain) {
			sendMail(from, "Error validating email address",
				fmt.Sprintf("email was sent from %q, but contents mention %q.", from, userName))
			return
		}
		// Otherwise, it's okay. The user owns the suffixed user name.
	}

	// Lookup userName. It must not exist yet.
	_, err = key.Lookup(userName)
	if err == nil || !errors.Match(errors.E(errors.NotExist), err) {
		// TODO: this leaks the fact that the user exists, even though we're not saying it.
		sendMail(from, "Can't create user", fmt.Sprintf("Can't create user for %s", userName))
		// Also log the error, if any.
		if err != nil {
			log.Error.Printf("Error looking up %q: %s", userName, err)
		}
		return
	}

	// Create user.
	u := &upspin.User{
		Name:      userName,
		PublicKey: pubKey,
	}
	err = createUser(u)
	if err != nil {
		sendMail(from, fmt.Sprintf("Failure creating Upspin user %q", userName), err.Error())
		return
	}

	sendMail(from, "Welcome to Upspin",
		fmt.Sprintf("Your account %q with public key\n%s\nhas been created.", userName, pubKey))
}

func createUser(u *upspin.User) error {
	// We need to dial this server locally so the new user is authenticated
	// with it implicitly.
	ctx := context.New()
	ctx = context.SetKeyEndpoint(ctx, key.Endpoint())
	ctx = context.SetUserName(ctx, u.Name)

	service, err := key.Dial(ctx, key.Endpoint())
	if err != nil {
		return err
	}
	keyServer, ok := service.(upspin.KeyServer)
	if !ok {
		return errors.E(errors.Internal, errors.Str("dialed service not an instance of upspin.KeyServer"))
	}
	return keyServer.Put(u)
}

func validateSignature(u upspin.UserName, k upspin.PublicKey, sig upspin.Signature) error {
	hash := []byte(string(u) + string(k))
	ecdsaPubKey, _, err := factotum.ParsePublicKey(k)
	if err != nil {
		return err
	}
	if !ecdsa.Verify(ecdsaPubKey, hash, sig.R, sig.S) {
		return errors.E(errors.Invalid, errors.Str("signature does not match"))
	}
	return nil
}

// sendMail sends email to a recipient with a given subject and contents.
// If email processing fails, it logs an error.
func sendMail(to, subject, contents string) {
	if email == nil {
		initEmail()
	}
	err := email.Send(to, serverName, subject, contents, noHTML)
	if err != nil {
		log.Error.Printf("Error sending email: %s", err)
	}
}

// emailLock is only needed when creating the email object the first time.
var emailLock sync.Mutex

func initEmail() {
	emailLock.Lock()
	if email == nil {
		email = sendgrid.New(*emailAPIKey, "upspin.io")
	}
	emailLock.Unlock()
}
