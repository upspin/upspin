// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"upspin.io/cloud/mail"
	"upspin.io/cloud/mail/sendgrid"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/serverutil"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"
)

const (
	// signupGracePeriod is the period of validity for a signup request.
	signupGracePeriod = 24 * time.Hour

	// signupNotifyAddress is the address that should receive signup notifications.
	signupNotifyAddress = "upspin-sendgrid@google.com"

	noHTML = "" // for mail.Send
)

// signupHandler implements an http.Handler that handles user creation requests
// made by 'upspin signup' and the user themselves.
type signupHandler struct {
	fact upspin.Factotum
	key  upspin.KeyServer
	mail mail.Mail

	rate serverutil.RateLimiter
}

// newSignupHandler creates a new handler that serves /signup.
func newSignupHandler(fact upspin.Factotum, key upspin.KeyServer, mailConfig string) (*signupHandler, error) {
	apiKey, _, _, err := parseMailConfig(mailConfig)
	if err != nil {
		return nil, err
	}
	m := &signupHandler{
		fact: fact,
		key:  key,
		mail: sendgrid.New(apiKey, "upspin.io"),
		rate: serverutil.RateLimiter{
			Backoff: 1 * time.Minute,
			Max:     24 * time.Hour,
		},
	}
	return m, nil
}

func (m *signupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	errorf := func(code int, format string, args ...interface{}) {
		s := fmt.Sprintf(format, args...)
		http.Error(w, s, code)
	}

	// Parse and validate request.
	v := r.FormValue
	u := &upspin.User{
		Name: upspin.UserName(v("name")),
		Dirs: []upspin.Endpoint{{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(v("dir")),
		}},
		Stores: []upspin.Endpoint{{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(v("store")),
		}},
		PublicKey: upspin.PublicKey(v("key")),
	}
	if err := valid.UserName(u.Name); err != nil {
		errorf(http.StatusBadRequest, "invalid user name")
		return
	}
	sigR, sigS, nowS := v("sigR"), v("sigS"), v("now")
	create := sigR+sigS+nowS != ""

	// Lookup userName. It must not exist yet.
	_, err := m.key.Lookup(u.Name)
	if err == nil {
		errorf(http.StatusBadRequest, "user already exists on key server")
		return
	} else if !errors.Match(errors.E(errors.NotExist), err) {
		errorf(http.StatusInternalServerError, "error looking up user: %v", err)
		return
	}

	if create {
		// This is the user clicking the link in the signup mail.
		// Validate the server signature and create the user.

		// Parse signature.
		var rs, ss big.Int
		if _, ok := rs.SetString(sigR, 10); !ok {
			errorf(http.StatusBadRequest, "invalid signature R value")
			return
		}
		if _, ok := ss.SetString(sigS, 10); !ok {
			errorf(http.StatusBadRequest, "invalid signature S value")
			return
		}
		sig := upspin.Signature{R: &rs, S: &ss}

		// Parse time.
		nowI, err := strconv.ParseInt(nowS, 10, 64)
		if err != nil {
			errorf(http.StatusBadRequest, "invalid now value: %v", err)
			return
		}
		now := time.Unix(nowI, 0)

		// Validate signature.
		if err := m.validateSignature(u, now, sig); err != nil {
			errorf(http.StatusBadRequest, "invalid signature: %v", err)
			return
		}

		// Create user.
		err = m.createUser(u)
		if err != nil {
			errorf(http.StatusInternalServerError, "could not create user: %v", err)
			return
		}

		// Send a note to our internal list, so we're aware of signups.
		subject := "New signup: " + string(u.Name)
		body := fmt.Sprintf("%s signed up on %s", u.Name, time.Now().Format(time.Stamp))
		err = m.mail.Send(signupNotifyAddress, serverName, subject, body, noHTML)
		if err != nil {
			log.Error.Printf("Error sending mail to %q: %v", signupNotifyAddress, err)
			// Don't prevent signup if this fails.
		}

		// TODO(adg): display user friendly welcome message
		fmt.Fprintf(w, "An account for %q has been registered with the key server.", u.Name)
		return
	}
	// We are being called by 'upspin signup'.

	if r.Method != "POST" {
		errorf(http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Aggressively rate limit requests to this service,
	// so that we can't be used for a mail bomb.
	// TODO(adg): also limit by remote IP address
	name, _, domain, err := user.Parse(u.Name)
	if err != nil {
		errorf(http.StatusBadRequest, "invalid user name: %v", err)
		return
	}
	key := strings.ToLower(name + "@" + domain)
	if ok, wait := m.rate.Pass(key); !ok {
		errorf(http.StatusTooManyRequests, "repeated signup attempt; please wait %v before trying again", wait)
		return
	}

	// Construct signed sign-up URL.
	// Important: the signaure must only be transmitted to the calling user
	// by email, as it is proof of ownership of that email address. We must
	// take care not to expose the signature in response to this request
	// (in an error message, for example).
	now := time.Now()
	sig, err := m.sign(u, now)
	if err != nil {
		errorf(http.StatusInternalServerError, "could not generate signature: %v", err)
		return
	}
	vals := url.Values{
		"name":  {string(u.Name)},
		"dir":   {string(u.Dirs[0].NetAddr)},
		"store": {string(u.Stores[0].NetAddr)},
		"key":   {string(u.PublicKey)},
		"sigR":  {sig.R.String()},
		"sigS":  {sig.S.String()},
		"now":   {fmt.Sprint(now.Unix())},
	}
	signupURL := (&url.URL{
		Scheme:   "https",
		Host:     "key.upspin.io", // TODO(adg): make configurable
		Path:     "/signup",
		RawQuery: vals.Encode(),
	}).String()

	// Send signup confirmation mail to user.
	body := new(bytes.Buffer)
	fmt.Fprintln(body, "Follow this link to complete the Upspin signup process:")
	fmt.Fprintln(body, signupURL)
	fmt.Fprintln(body, "\nIf you were not expecting this message, please ignore it.")
	// TODO(adg): implement opt out link
	const subject = "Upspin signup confirmation"
	err = m.mail.Send(string(u.Name), serverName, subject, body.String(), noHTML)
	if err != nil {
		log.Error.Printf("Error sending mail to %q: %v", u.Name, err)
		errorf(http.StatusInternalServerError, "could not send signup email")
		return
	}

	fmt.Fprintln(w, "OK")
}

func (m *signupHandler) createUser(u *upspin.User) error {
	key, err := m.dialForUser(u.Name)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.Put(u); err != nil {
		return err
	}

	snapshotUser, err := snapshotUser(u.Name)
	if err != nil {
		return err
	}

	// Lookup snapshotUser to ensure we don't overwrite an existing one.
	_, err = key.Lookup(snapshotUser)
	if err != nil && !errors.Match(errors.E(errors.NotExist), err) {
		return err
	}
	if err == nil {
		// Snapshot user exists; no need to create it.
		return nil
	}
	// Create snapshot user.
	key, err = m.dialForUser(snapshotUser)
	if err != nil {
		return err
	}
	defer key.Close() // be nice and release resources.
	return key.Put(&upspin.User{
		Name:      snapshotUser,
		PublicKey: u.PublicKey,
	})
}

func (m *signupHandler) dialForUser(name upspin.UserName) (upspin.KeyServer, error) {
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

// sign generates a signature for the given user creation request at time now.
func (m *signupHandler) sign(u *upspin.User, now time.Time) (upspin.Signature, error) {
	b, err := sigBytes(u, now)
	if err != nil {
		return upspin.Signature{}, err
	}
	return m.fact.Sign(b)
}

func (m *signupHandler) validateSignature(u *upspin.User, now time.Time, sig upspin.Signature) error {
	// Check that the signature is still valid.
	if time.Now().After(now.Add(signupGracePeriod)) {
		return errors.Str("request too old; please try again")
	}
	b, err := sigBytes(u, now)
	if err != nil {
		return err
	}
	return factotum.Verify(b, sig, m.fact.PublicKey())
}

func sigBytes(u *upspin.User, now time.Time) ([]byte, error) {
	b, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	b = strconv.AppendInt(b, now.Unix(), 10)
	h := sha256.Sum256(b)
	return h[:], nil
}

func parseMailConfig(name string) (apiKey, userName, password string, err error) {
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
