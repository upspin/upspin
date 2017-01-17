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
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"text/template"
	"time"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
	inbound "upspin.io/cmd/keyserver/internal/mail"
	"upspin.io/config"
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
		return nil, errors.E(errors.Invalid, errors.Str("mailHandler: key server must be provided"))
	}
	apiKey, userName, password, err := parseConfigFile(configFile)
	if err != nil {
		return nil, errors.E("mailHandler", err)
	}
	m := &mailHandler{
		key:      key,
		userName: userName,
		password: password,
		out:      sendgrid.New(apiKey, "upspin.io"),
	}
	return m, nil
}

func (m *mailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caller, pwd, ok := r.BasicAuth()
	if !ok {
		http.Error(w, "authentication failed", http.StatusForbidden)
		log.Error.Printf("mailHandler: No authentication provided by remote: %s", r.RemoteAddr)
		return
	}
	if pwd != m.password || caller != m.userName {
		http.Error(w, "authentication failed", http.StatusForbidden)
		log.Error.Println("mailHandler: Error in basic auth: invalid credentials")
		return
	}

	// From here on, we accepted the delivery of the incoming email. Now
	// parse its headers and contents.
	w.WriteHeader(http.StatusOK)

	// Now begin parsing it.
	err := r.ParseMultipartForm(64 * 1024) // 64kB budget for email parsing.
	if err != nil {
		log.Error.Printf("mailHandler: Error parsing request: %v", err)
		return
	}

	// TODO: here we trust the email processor; we should do DKIM validation
	// ourselves.
	from, body, err := inbound.ParseMail(r.Form)
	if err != nil {
		// Error validating email. Maybe 'from' is known but without
		// a proper DKIM verification we can't reply to the sender since
		// it could be spoofed. Log and drop it on the floor.
		log.Printf("mailHandler: Received email from %q: %s", from, err)
		return
	}
	// From address is valid. Further errors can be replied by email.

	// Parse the contents of the email. The returned user name is guaranteed
	// to be a valid Upspin user name.
	msg, err := inbound.ParseBody(body)
	if err != nil {
		m.sendErrorMail(from, "Invalid email contents", err)
		return
	}

	// Perform the following tasks next:
	// 1) signature validation
	// 2) compare userName with from address. Must be identical except for
	//    an optional suffix.
	// 3) verify userName does not yet exist.
	if err := validateSignature(msg); err != nil {
		m.sendErrorMail(from, "Error validating signature", err)
		return
	}
	// Username and email must match. We don't allow email signups to come
	// from different accounts or to use a suffix (for suffixes, the owner
	// of the canonical user can use 'upspin user -put' to create it).
	if string(msg.UserName) != from {
		m.sendErrorMail(from, "Error validating email address",
			errors.Errorf("email was sent from %q, but contents mention %q.", from, msg.UserName))
		return
	}

	// Lookup userName. It must not exist yet.
	_, err = m.key.Lookup(msg.UserName)
	if err == nil || !errors.Match(errors.E(errors.NotExist), err) {
		m.sendErrorMail(from, "Can't create user", errors.Errorf("Can't create user for %s", msg.UserName))
		// Also log the error, if any.
		if err != nil {
			log.Error.Printf("mailHandler: Error looking up %q: %s", msg.UserName, err)
		}
		return
	}

	// Create user.
	err = m.createUser(msg)
	if err != nil {
		m.sendErrorMail(from, fmt.Sprintf("Failure creating Upspin user %q", msg.UserName), err)
		return
	}

	m.sendWelcomeEmail(msg)
}

func (m *mailHandler) createUser(msg *inbound.SignupMessage) error {
	key, err := m.dialForUser(msg.UserName)
	if err != nil {
		return err
	}
	defer key.Close() // be nice and release resources.
	err = key.Put(&upspin.User{
		Name:      msg.UserName,
		PublicKey: msg.PublicKey,
		Dirs:      []upspin.Endpoint{msg.Dir},
		Stores:    []upspin.Endpoint{msg.Store},
	})
	if err != nil {
		return err
	}

	snapshotUser, err := snapshotUser(msg.UserName)
	if err != nil {
		return err
	}

	// Lookup snapshotUser to ensure we don't overwrite an existing one.
	_, err = m.key.Lookup(snapshotUser)
	if err != nil && !errors.Match(errors.E(errors.NotExist), err) {
		return err
	}
	if err == nil {
		// We do not update the snapshot user if it already exists.
		log.Printf("Attempt to re-create an existing user: %s", snapshotUser)
		return nil
	}
	// Else, if it does not exist, we create it.
	keySnap, err := m.dialForUser(snapshotUser)
	if err != nil {
		return err
	}
	defer keySnap.Close() // be nice and release resources.
	return keySnap.Put(&upspin.User{
		Name:      snapshotUser,
		PublicKey: msg.PublicKey,
	})
}

func (m *mailHandler) dialForUser(name upspin.UserName) (upspin.KeyServer, error) {
	// We need to dial this server locally so the new user is authenticated
	// with it implicitly.
	cfg := config.New()
	cfg = config.SetKeyEndpoint(cfg, m.key.Endpoint())
	cfg = config.SetUserName(cfg, name)

	service, err := m.key.Dial(cfg, m.key.Endpoint())
	if err != nil {
		return nil, err
	}
	keyServer, ok := service.(upspin.KeyServer)
	if !ok {
		return nil, errors.E(errors.Internal, errors.Str("dialed service not an instance of upspin.KeyServer"))
	}
	return keyServer, nil
}

func validateSignature(msg *inbound.SignupMessage) error {
	input := []byte(string(msg.UserName) + string(msg.PublicKey) + msg.Dir.String() + msg.Store.String())
	return factotum.Verify(input, msg.Signature, msg.PublicKey)
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
	// TODO: don't send raw error messages (maybe a privacy/security issue).
	body := fmt.Sprintf("Failure signing up for Upspin: %s\n%s\n-- The Upspin team", reason, err.Error())
	m.sendMail(to, "Sign-up error", body)
}

// sendWelcomeEmail sends a welcome email to the user confirming the creation of
// the account with a given public key.
func (m *mailHandler) sendWelcomeEmail(msg *inbound.SignupMessage) {
	to := string(msg.UserName)

	var body bytes.Buffer
	if err := replyTemplate.Execute(&body, msg.UserName); err != nil {
		m.sendErrorMail(to, "An internal error occurred", err)
		return
	}

	m.sendMail(to, "Welcome to Upspin", body.String())

	// Send a note to our internal list, so we're aware of signups.
	m.sendMail("upspin-sendgrid@google.com", "New signup: "+string(msg.UserName),
		fmt.Sprintf("%s successfully signed up on %s", msg.UserName, time.Now().Format(time.UnixDate)))
}

func parseConfigFile(name string) (apiKey, userName, password string, err error) {
	data, err := ioutil.ReadFile(name)
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

// snapshotUser returns the snapshot username for the named user.
func snapshotUser(u upspin.UserName) (upspin.UserName, error) {
	// Attempt to create a "+snapshot" user.
	name, suffix, domain, err := user.Parse(u)
	if err != nil {
		return "", err
	}
	if suffix != "" {
		name = name[:len(name)-len(suffix)-1]
	}
	return upspin.UserName(name + "+snapshot@" + domain), nil
}

// replyTemplate is the email message we send back to the user after the
// account is created.
var replyTemplate = template.Must(template.New("reply").Parse(`
Your Upspin account {{.}}
has been registered with the public key server.

For instructions on setting up your directory tree, see TODO.
`))
