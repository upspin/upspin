// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sendgrid

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"upspin.io/errors"
)

func TestSend(t *testing.T) {
	var request []byte
	var header http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		request, err = ioutil.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(503)
			return
		}
		header = r.Header
		r.Body.Close()
		w.WriteHeader(http.StatusAccepted)
	}))

	prev := apiSend
	defer func() { apiSend = prev }()
	apiSend = ts.URL

	const (
		key       = "mykey"
		to        = "somewhere@example.net"
		from      = "me@example.com"
		subject   = "hello"
		textBody  = "text"
		htmlBody  = "html"
		goldenReq = `{"Personalizations":[{"To":[{"Email":"somewhere@example.net","Name":""}],"Subject":"hello"}],"From":{"Email":"me@example.com","Name":""},"Content":[{"Type":"text/plain","Value":"text"},{"Type":"text/html","Value":"html"}]}`
	)
	sg := New(key)

	err := sg.Send(to, from, subject, textBody, htmlBody)
	if err != nil {
		t.Fatal(err)
	}

	if string(request) != goldenReq {
		t.Fatalf("req sent = %q, want = %q", request, goldenReq)
	}
	if got, want := header.Get("Authorization"), "Bearer "+key; want != got {
		t.Fatalf("auth = %q, want = %q", got, want)
	}
}

func TestSendError(t *testing.T) {
	sg := New("mykey")

	err := sg.Send("to@you.com", "from_me", "hello", "", "")
	expectedErr := errors.E(errors.Invalid, "text or html body must be provided")
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %q, want = %q", err, expectedErr)
	}
}
