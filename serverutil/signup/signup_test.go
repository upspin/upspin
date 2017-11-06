// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package signup

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/key/inprocess"
	"upspin.io/test/testutil"
	"upspin.io/upspin"
)

func TestSignup(t *testing.T) {
	// Set up a signup handler in a test HTTP server.
	serverFact, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "test"))
	if err != nil {
		t.Fatal(err)
	}
	key := inprocess.New()
	mail := &mailStub{}
	mc := MailConfig{
		Project: "test",
		Mail:    mail,
		Notify:  "signup@noti.fy",
	}
	h := NewHandler("will-be-overridden", serverFact, key, &mc)
	s := httptest.NewServer(h)
	defer s.Close()
	h.(*handler).baseURL = s.URL

	signupURLScheme = "http"
	defer func() {
		signupURLScheme = "https"
	}()
	keyServer := upspin.NetAddr(strings.TrimPrefix(s.URL, "http://"))

	userName := upspin.UserName("bob@example.com")

	// Make a signup request.
	for _, eps := range [][2]upspin.NetAddr{
		{"dir.example.com:443", "store.example.com:443"},
		{"", ""},
	} {
		dirServer, storeServer := eps[0], eps[1]

		cfg := config.New()
		cfg = config.SetUserName(cfg, userName)
		userFact, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "bob"))
		if err != nil {
			t.Fatal(err)
		}
		cfg = config.SetFactotum(cfg, userFact)
		cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   keyServer,
		})
		cfg = config.SetDirEndpoint(cfg, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   dirServer,
		})
		cfg = config.SetStoreEndpoint(cfg, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   storeServer,
		})
		if err := MakeRequest(cfg); err != nil {
			t.Fatal(err)
		}

		// Simulate user clicking the verification link in the email.
		if len(mail.text) != 1 {
			t.Fatalf("got %d mail messages, want 1", len(mail.text))
		}
		i := strings.Index(mail.text[0], s.URL)
		if i == -1 {
			t.Fatalf("could not find signup URL in mail text %q", mail.text[0])
		}
		url := mail.text[0][i:]
		i = strings.Index(url, "\n")
		if i == -1 {
			t.Fatalf("could not find trailing newline after URL in mail text %q", url)
		}
		url = url[:i]
		r, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(r.Body)
			t.Fatalf("%s: %s", r.Status, b)
		}

		// Check that the user is now in the key server.
		u, err := key.Lookup(userName)
		if err != nil {
			t.Fatal(err)
		}
		if u.Name != userName {
			t.Errorf("got username %q, want %q", u.Name, userName)
		}
		if dirServer != "" && (len(u.Dirs) == 0 || u.Dirs[0].NetAddr != dirServer) {
			t.Errorf("got dirs %v, want %q", u.Dirs, dirServer)
		}
		if storeServer != "" && (len(u.Stores) == 0 || u.Stores[0].NetAddr != storeServer) {
			t.Errorf("got stores %v, want %q", u.Stores, storeServer)
		}
		if got, want := u.PublicKey, userFact.PublicKey(); got != want {
			t.Errorf("got public key %q, want %q", got, want)
		}

		// Check that the signup notification was sent.
		if len(mail.text) != 2 {
			t.Fatalf("got %d mail messages, want 2", len(mail.text))
		}
		if !strings.Contains(mail.text[1], string(userName)) {
			t.Errorf("signup notification %q does not contain %q", mail.text[1], userName)
		}

		userName = "yob-" + userName
		mail.text = nil
	}
}

// mailStub is an implementation of mail.Mail that simply stores the text of
// the sent messages.
type mailStub struct {
	text []string
}

func (m *mailStub) Send(to, from, subject, text, html string) error {
	m.text = append(m.text, text)
	return nil
}
