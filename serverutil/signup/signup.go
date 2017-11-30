// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package signup provides an http.Handler implementation that serves and
// validates KeyServer signup requests.
package signup

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
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
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/rpc"
	"upspin.io/serverutil"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"
)

const (
	// signupGracePeriod is the period of validity for a signup request.
	signupGracePeriod = 24 * time.Hour

	noHTML = "" // for mail.Send
)

// handler implements an http.Handler that handles user creation requests
// made by 'upspin signup' and the user themselves.
type handler struct {
	baseURL string
	fact    upspin.Factotum
	key     upspin.KeyServer
	mail    *MailConfig

	rate serverutil.RateLimiter
}

// MailConfig holds the mail configuration used by the signup handler.
type MailConfig struct {
	// Mail holds the mailer used for sending signup emails and notifications.
	mail.Mail

	// Project is the name used in the subject line of signup notifications,
	// to distinguish between test and production keyserver instances.
	Project string

	// Notify specifies the recipient address for signup notifications.
	Notify string

	// From specifies the address from which to send mail messages.
	From string
}

// NewHandler creates a new handler that serves signup requests (made by
// 'upspin signup') and verification requests (visited by clicking the link in
// the email).
// The Factotum is used to sign the verification URL. The KeyServer is where
// the new user will be created. The MailConfig is used to send mail.
func NewHandler(baseURL string, fact upspin.Factotum, key upspin.KeyServer, mc *MailConfig) http.Handler {
	return &handler{
		baseURL: baseURL,
		fact:    fact,
		key:     key,
		mail:    mc,
		rate: serverutil.RateLimiter{
			Backoff: 1 * time.Minute,
			Max:     24 * time.Hour,
		},
	}
}

func (m *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	errorf := func(code int, format string, args ...interface{}) {
		s := fmt.Sprintf(format, args...)
		http.Error(w, s, code)
	}

	// Parse and validate request.
	v := r.FormValue
	name, dir, store, pkey, sigR, sigS := v("name"), v("dir"), v("store"), v("key"), v("sigR"), v("sigS")
	u := &upspin.User{
		Name:      upspin.UserName(name),
		PublicKey: upspin.PublicKey(pkey),
	}
	if dir != "" {
		u.Dirs = []upspin.Endpoint{{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(dir),
		}}
	}
	if store != "" {
		u.Stores = []upspin.Endpoint{{
			Transport: upspin.Remote,
			NetAddr:   upspin.NetAddr(store),
		}}
	}
	if err := valid.UserName(u.Name); err != nil {
		errorf(http.StatusBadRequest, "invalid user name: %s", u.Name)
		return
	}
	_, suffix, _, _ := user.Parse(u.Name) // valid already checked the error.
	if suffix != "" {
		errorf(http.StatusBadRequest, "user name must not be suffixed: %s", u.Name)
		return
	}

	// Lookup userName. It must not exist yet.
	_, err := m.key.Lookup(u.Name)
	if err == nil {
		errorf(http.StatusBadRequest, "user already exists on key server: %s", u.Name)
		return
	} else if !errors.Is(errors.NotExist, err) {
		errorf(http.StatusInternalServerError, "error looking up user: %v", err)
		return
	}

	nowS := v("now")
	create := nowS != ""

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
			log.Error.Printf("signup: error creating user %#v: %v", u, err)
			errorf(http.StatusInternalServerError, "could not create user: %v", err)
			return
		}

		subject := fmt.Sprintf("New signup on %s: %s", m.mail.Project, string(u.Name))
		body := fmt.Sprintf("%s signed up on %s on %s", u.Name, m.mail.Project, time.Now().Format(time.Stamp))
		err = m.mail.Send(m.mail.Notify, m.mail.From, subject, body, noHTML)
		if err != nil {
			log.Error.Printf("signup: error sending mail to %q: %v", m.mail.Notify, err)
			// Don't prevent signup if this fails.
		}
		log.Info.Printf("signup: registration complete for %q", u.Name)

		// TODO(adg): display user friendly welcome message
		fmt.Fprintf(w, "An account for %q has been registered with the key server.", u.Name)
		return
	}
	// We are being called by 'upspin signup'.

	if err := verifySignupSignature(name, dir, store, pkey, sigR, sigS); err != nil {
		errorf(http.StatusBadRequest, "invalid request: %s", err)
		return
	}
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
	dir, store = "", ""
	if len(u.Dirs) == 1 {
		dir = string(u.Dirs[0].NetAddr)
	}
	if len(u.Stores) == 1 {
		store = string(u.Stores[0].NetAddr)
	}
	vals := url.Values{
		"name":  {string(u.Name)},
		"dir":   {dir},
		"store": {store},
		"key":   {string(u.PublicKey)},
		"sigR":  {sig.R.String()},
		"sigS":  {sig.S.String()},
		"now":   {fmt.Sprint(now.Unix())},
	}
	signupURL := m.baseURL + "?" + vals.Encode()

	// Send signup confirmation mail to user.
	body := new(bytes.Buffer)
	fmt.Fprintln(body, "Follow this link to complete the Upspin signup process:")
	fmt.Fprintln(body, signupURL)
	fmt.Fprintln(body, "\nIf you were not expecting this message, please ignore it.")
	// TODO(adg): implement opt out link
	const subject = "Upspin signup confirmation"
	err = m.mail.Send(string(u.Name), m.mail.From, subject, body.String(), noHTML)
	if err != nil {
		log.Error.Printf("signup: error sending mail to %q: %v", u.Name, err)
		errorf(http.StatusInternalServerError, "could not send signup email")
		return
	}
	log.Info.Printf("signup: sent message to %q", u.Name)

	fmt.Fprintln(w, "OK")
}

// verifySignupSignature verifies that the new user record comprised of name,
// dir, store and key were properly signed by the new user using the private key
// that corresponds to the public key provided.
func verifySignupSignature(user, dir, store, key, sigR, sigS string) error {
	var rs, ss big.Int
	if _, ok := rs.SetString(sigR, 10); !ok {
		return errors.Str("invalid signature R value")
	}
	if _, ok := ss.SetString(sigS, 10); !ok {
		return errors.Str("invalid signature S value")
	}
	sig := upspin.Signature{R: &rs, S: &ss}
	hash, _ := RequestHash(upspin.UserName(user), upspin.NetAddr(dir), upspin.NetAddr(store), upspin.PublicKey(key))
	return factotum.Verify(hash, sig, upspin.PublicKey(key))
}

func (m *handler) createUser(u *upspin.User) error {
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
	if err != nil && !errors.Is(errors.NotExist, err) {
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

func (m *handler) dialForUser(name upspin.UserName) (upspin.KeyServer, error) {
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
		return nil, errors.E(errors.Internal, "dialed service not an instance of upspin.KeyServer")
	}
	return keyServer, nil
}

// sign generates a signature for the given user creation request at time now.
func (m *handler) sign(u *upspin.User, now time.Time) (upspin.Signature, error) {
	b, err := sigBytes(u, now)
	if err != nil {
		return upspin.Signature{}, err
	}
	return m.fact.Sign(b)
}

func (m *handler) validateSignature(u *upspin.User, now time.Time, sig upspin.Signature) error {
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

var signupURLScheme = "https" // Tests may override this.

// MakeRequest sends a signup request for the given Config to the Config's
// KeyServer Endpoint using the Config's TLS certs (if any).
func MakeRequest(cfg upspin.Config) error {
	query, err := makeQueryString(cfg)
	if err != nil {
		return err
	}
	certPool, err := rpc.CertPoolFromConfig(cfg)
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: certPool,
			},
		},
	}
	signupURL := fmt.Sprintf("%s://%s/signup", signupURLScheme, cfg.KeyEndpoint().NetAddr)
	r, err := client.Post(signupURL+"?"+query, "text/plain", nil)
	if err != nil {
		return err
	}
	b, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return err
	}
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("key server error: %s", b)
	}
	return nil
}

// makeQueryString returns an encoded query string used to sign up a new user
// with the KeyServer.
func makeQueryString(cfg upspin.Config) (string, error) {
	f := cfg.Factotum()
	if f == nil {
		return "", errors.Str("cannot sign up without Factotum")
	}
	hash, vals := RequestHash(cfg.UserName(), cfg.DirEndpoint().NetAddr, cfg.StoreEndpoint().NetAddr, cfg.Factotum().PublicKey())
	sig, err := cfg.Factotum().Sign(hash)
	if err != nil {
		return "", err
	}
	vals.Add("sigR", sig.R.String())
	vals.Add("sigS", sig.S.String())
	return vals.Encode(), nil
}

// RequestHash generates a hash of the supplied arguments that, when signed, is
// used to prove that a signup request originated from the user that owns the
// supplied private key.
func RequestHash(name upspin.UserName, dir, store upspin.NetAddr, key upspin.PublicKey) ([]byte, url.Values) {
	const magic = "signup-request"

	u := url.Values{}
	h := sha256.New()
	h.Write([]byte(magic))
	w := func(key, val string) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(val)))
		h.Write(l[:])
		h.Write([]byte(val))
		u.Add(key, val)
	}
	w("name", string(name))
	w("dir", string(dir))
	w("store", string(store))
	w("key", string(key))
	return h.Sum(nil), u
}
