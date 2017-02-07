// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
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
	"upspin.io/upspin"
	"upspin.io/user"
)

// signupGracePeriod is the period of validity for a signup request.
const signupGracePeriod = 24 * time.Hour

// signupHandler implements an http.Handler that handles user creation requests
// made by 'upspin signup' and the user themselves.
type signupHandler struct {
	cfg  upspin.Config // KeyServer user
	key  upspin.KeyServer
	mail mail.Mail
}

// newSignupHandler creates a new handler
func newSignupHandler(cfg upspin.Config, key upspin.KeyServer, mailConfig string) (*signupHandler, error) {
	apiKey, _, _, err := parseMailConfig(mailConfig)
	if err != nil {
		return nil, err
	}
	if cfg.Factotum() == nil {
		return nil, errors.Str("signupHandler: provided Config must have a Factotum")
	}
	m := &signupHandler{
		cfg:  cfg,
		key:  key,
		mail: sendgrid.New(apiKey, "upspin.io"),
	}
	return m, nil
}

func (m *signupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	errorf := func(code int, format string, args ...interface{}) {
		s := fmt.Sprintf(format, args...)
		http.Error(w, s, code)
	}

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
	sigR, sigS, nowS := v("sigR"), v("sigS"), v("now")

	if sigR != "" && sigS != "" && nowS != "" {
		// This is the user clicking the link in their signup mail.
		// Validate the server signature and create the user.

		// TODO(adg): check this is a GET

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

		// TODO(adg): display user friendly welcome message
		return
	}
	// We are being called by 'upspin signup'.
	log.Printf("signup %#v", u)

	// TODO(adg): check this is a POST
	// TODO(adg): rate limit

	// Lookup userName. It must not exist yet.
	// TODO(adg): do this for both variants of this call
	_, err := m.key.Lookup(u.Name)
	if err == nil {
		errorf(http.StatusBadRequest, "user already exists on key server")
		return
	} else if !errors.Match(errors.E(errors.NotExist), err) {
		errorf(http.StatusInternalServerError, "error looking up user: %v", err)
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

	log.Printf("signup URL: %v", signupURL)

	// TODO(adg): send mail

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

// sign generates a signature for the given user creation request at now.
func (m *signupHandler) sign(u *upspin.User, now time.Time) (upspin.Signature, error) {
	b, err := sigBytes(u, now)
	if err != nil {
		return upspin.Signature{}, err
	}
	return m.cfg.Factotum().Sign(b)
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
	return factotum.Verify(b, sig, m.cfg.Factotum().PublicKey())
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

// sendMail sends email to a recipient with a given subject and contents.
func (m *signupHandler) sendMail(to, subject, contents string) error {
	const noHTML = ""
	return m.mail.Send(to, serverName, subject, contents, noHTML)
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
