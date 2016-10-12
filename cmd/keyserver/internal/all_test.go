// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func TestParseMail(t *testing.T) {
	req := newRequest(t, emailPassesDKIM)
	from, body, err := ParseMail(req.Form)
	if err != nil {
		t.Fatal(err)
	}
	expected := "dude@gmail.com"
	if from != expected {
		t.Errorf("from = %q, want = %q", from, expected)
	}
	expected = "Test body!\n"
	if body != expected {
		t.Errorf("body = %q, want = %q", body, expected)
	}
}

func TestParseMailFailDKIM_DomainMismatch(t *testing.T) {
	req := newRequest(t, emailDKIMDomainMismatch)
	_, _, err := ParseMail(req.Form)
	expectedErr := errors.E(errors.Permission, errors.Str(`DKIM not present for domain "vanity.org"`))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %s", err, expectedErr)
	}
}

func TestParseMailFailDKIM_HardFailure(t *testing.T) {
	failDKIM := strings.Replace(emailPassesDKIM, "@gmail.com : pass", "@gmail.com : fail", -1)
	req := newRequest(t, failDKIM)
	_, _, err := ParseMail(req.Form)
	expectedErr := errors.E(errors.Permission, errors.Str(`DKIM failed for domain "gmail.com"`))
	if !errors.Match(expectedErr, err) {
		t.Fatalf("err = %v, want = %s", err, expectedErr)
	}
}

func TestParseBody(t *testing.T) {
	const valid = `
Some garbage
-----
I'm foo@bar.com
My public key is
p256
1063349993423423435345345345345345340
3453453457828271720003453453245354698
Signature: 1223234235345834578abc
--- more garbage`

	userName, pubKey, sig, err := ParseBody(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := userName, upspin.UserName("foo@bar.com"); got != want {
		t.Errorf("userName = %q, want = %q", got, want)
	}
	if got, want := pubKey, upspin.PublicKey("p256\n1063349993423423435345345345345345340\n3453453457828271720003453453245354698"); got != want {
		t.Errorf("pubKey = %q, want = %q", got, want)
	}
	if got, want := sig, "1223234235345834578abc"; got != want {
		t.Errorf("sig = %q, want = %q", got, want)
	}
}

func newRequest(t *testing.T, data string) *http.Request {
	req, err := http.NewRequest("POST", "some.url/incoming", bytes.NewReader([]byte(data)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xYzZY")
	err = req.ParseMultipartForm(1024)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// These requests are real, abbreviated incoming messages with some of the
// contents changed or simplified.
const emailDKIMDomainMismatch = `--xYzZY
Content-Disposition: form-data; name="headers"

MIME-Version: 1.0
Sender: zardoz@gmail.com
Received: by 10.13.226.132 with HTTP; Tue, 11 Oct 2016 14:00:24 -0700 (PDT)
From: Zardoz <zardoz@vanity.org>
Date: Tue, 11 Oct 2016 14:00:24 -0700
Subject: the rain in spain
To: foo@key.upspin.io

--xYzZY
Content-Disposition: form-data; name="dkim"

{@gmail.com : pass}
--xYzZY
Content-Disposition: form-data; name="to"

foo@key.upspin.io
--xYzZY
Content-Disposition: form-data; name="html"

<div dir="ltr">falls on my head</div>

--xYzZY
Content-Disposition: form-data; name="from"

Zardoz <zardoz@vanity.org>
--xYzZY
Content-Disposition: form-data; name="text"

falls on my head

--xYzZY
Content-Disposition: form-data; name="sender_ip"

209.85.213.182
--xYzZY
Content-Disposition: form-data; name="envelope"

{"to":["foo@key.upspin.io"],"from":"zardoz@gmail.com"}
--xYzZY
Content-Disposition: form-data; name="attachments"

0
--xYzZY
Content-Disposition: form-data; name="subject"

the rain in spain
--xYzZY
Content-Disposition: form-data; name="spam_score"

0.011
--xYzZY
Content-Disposition: form-data; name="charsets"

{"to":"UTF-8","html":"UTF-8","subject":"UTF-8","from":"UTF-8","text":"UTF-8"}
--xYzZY
Content-Disposition: form-data; name="SPF"

pass
--xYzZY--

`

const emailPassesDKIM = `--xYzZY
Content-Disposition: form-data; name="headers"

Received: by 10.13.227.130 with HTTP; Tue, 11 Oct 2016 13:45:32 -0700 (PDT)
From: Some Dude <dude@gmail.com>
Date: Tue, 11 Oct 2016 13:45:32 -0700
Subject: subject!
To: foo@key.upspin.io

--xYzZY
Content-Disposition: form-data; name="dkim"

{@gmail.com : pass}
--xYzZY
Content-Disposition: form-data; name="to"

foo@key.upspin.io
--xYzZY
Content-Disposition: form-data; name="html"

<div dir="ltr">Test body!</div>

--xYzZY
Content-Disposition: form-data; name="from"

Some Dude <dude@gmail.com>
--xYzZY
Content-Disposition: form-data; name="text"

Test body!

--xYzZY
Content-Disposition: form-data; name="envelope"

{"to":["foo@key.upspin.io"],"from":"dude@gmail.com"}
--xYzZY
Content-Disposition: form-data; name="attachments"

0
--xYzZY
Content-Disposition: form-data; name="subject"

subject!
--xYzZY
Content-Disposition: form-data; name="spam_score"

0.022
--xYzZY
Content-Disposition: form-data; name="SPF"

pass
--xYzZY--
`
