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
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
	inbound "upspin.io/cmd/keyserver/internal/mail"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
)

// mailHandler handles incoming signup email.
type mailHandler struct {
	key upspin.KeyServer

	// information to authenticate the incoming email provider.
	userName string
	password string

	// out is the outbound mail API.
	out mail.Mail
}

// newMailHandler creates a new handler interfacing with the given key server
// and configured as named by the configFile.
func newMailHandler(key upspin.KeyServer, configFile string) (*mailHandler, error) {
	if key == nil {
		return nil, errors.E(errors.Invalid, errors.Str("key server must be provided"))
	}
	apiKey, userName, password, err := parseConfigFile(configFile)
	if err != nil {
		return nil, err
	}
	m := &mailHandler{
		key:      key,
		userName: userName,
		password: password,
		out:      sendgrid.New(apiKey, "upspin.io"),
	}
	return m, nil
}

func (m *mailHandler) h(w http.ResponseWriter, r *http.Request) {
	caller, pwd, ok := r.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusForbidden)
		http.Error(w, "authentication failed", http.StatusForbidden)
		log.Error.Printf("No authentication provided by remote: %s", r.RemoteAddr)
		return
	}
	if pwd != m.password || caller != m.userName {
		http.Error(w, "authentication failed", http.StatusForbidden)
		log.Error.Println("Error in basic auth: invalid credentials")
		return
	}

	// From here on, we accepted the delivery of the incoming email. Now
	// parse its headers and contents.
	w.WriteHeader(http.StatusOK)

	// Now begin parsing it.
	err := r.ParseMultipartForm(64 * 1024) // 64kB budget for email parsing.
	if err != nil {
		log.Error.Printf("Error parsing request: %v", err)
		return
	}

	// TODO: here we trust the email processor; we should do DKIM validation
	// ourselves.
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
		m.sendErrorMail(from, "Invalid email contents", err)
		return
	}

	// Perform the following tasks next:
	// 1) signature validation
	// 2) compare userName with from address. Must be identical except for
	//    an optional suffix.
	// 3) verify userName does not yet exist.
	if err := validateSignature(userName, pubKey, sig); err != nil {
		m.sendErrorMail(from, "Error validating signature", err)
		return
	}
	if string(userName) != from {
		// Maybe they don't match because of a suffix.
		// Test that hypothesis here.
		u, _, domain, err := user.Parse(userName)
		if err != nil {
			m.sendErrorMail(from, "Error validating email address", err)
			return
		}
		if from != fmt.Sprintf("%s@%s", u, domain) {
			m.sendErrorMail(from, "Error validating email address",
				errors.Errorf("email was sent from %q, but contents mention %q.", from, userName))
			return
		}
		// Otherwise, it's okay. The user owns the suffixed user name.
	}

	// Lookup userName. It must not exist yet.
	_, err = m.key.Lookup(userName)
	if err == nil || !errors.Match(errors.E(errors.NotExist), err) {
		m.sendErrorMail(from, "Can't create user", errors.Errorf("Can't create user for %s", userName))
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
	err = m.createUser(u)
	if err != nil {
		m.sendErrorMail(from, fmt.Sprintf("Failure creating Upspin user %q", userName), err)
		return
	}

	m.sendMail(from, "Welcome to Upspin",
		fmt.Sprintf("Your account %q with public key\n%s\nhas been created.", userName, pubKey))
}

func (m *mailHandler) createUser(u *upspin.User) error {
	// We need to dial this server locally so the new user is authenticated
	// with it implicitly.
	ctx := context.New()
	ctx = context.SetKeyEndpoint(ctx, m.key.Endpoint())
	ctx = context.SetUserName(ctx, u.Name)

	service, err := m.key.Dial(ctx, m.key.Endpoint())
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
func (m *mailHandler) sendMail(to, subject, contents string) {
	const noHTML = ""
	err := m.out.Send(to, serverName, subject, contents, noHTML)
	if err != nil {
		log.Error.Printf("Error sending email: %s", err)
	}
}

func (m *mailHandler) sendErrorMail(to, reason string, err error) {
	m.sendMail(to, "Sign-up error",
		fmt.Sprintf("Failure signing up for upspin: %s\n%s\n-- The upspin team", reason, err.Error()))
}

func parseConfigFile(name string) (apiKey, userName, password string, err error) {
	var data []byte
	data, err = ioutil.ReadFile(name)
	if err != nil {
		return "", "", "", errors.E(errors.IO, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		return "", "", "", errors.E(errors.IO, errors.Str("config file must have 3 entries: api key, user name, password"))
	}
	apiKey = strings.TrimSpace(lines[0])
	userName = strings.TrimSpace(lines[1])
	password = strings.TrimSpace(lines[2])
	return
}
