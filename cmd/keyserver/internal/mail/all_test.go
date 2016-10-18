// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mail

import (
	"bytes"
	"math/big"
	"net/http"
	"reflect"
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

I am foo@bar.com
My public key is
p256
1063349993423423435345345345345345340
3453453457828271720003453453245354698
Signature:
122323423:199993211232983

`

	userName, pubKey, sig, err := ParseBody(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := userName, upspin.UserName("foo@bar.com"); got != want {
		t.Errorf("userName = %q, want = %q", got, want)
	}
	if got, want := pubKey, upspin.PublicKey("p256\n1063349993423423435345345345345345340\n3453453457828271720003453453245354698\n"); got != want {
		t.Errorf("pubKey = %q, want = %q", got, want)
	}
	var rs, ss big.Int
	if _, ok := rs.SetString("122323423", 10); !ok {
		t.Fatal("cannot parse R signature")
	}
	if _, ok := ss.SetString("199993211232983", 10); !ok {
		t.Fatal("cannot parse S signature")
	}
	expectedSig := upspin.Signature{
		R: &rs,
		S: &ss,
	}
	if got, want := sig, expectedSig; !reflect.DeepEqual(got, want) {
		t.Errorf("sig = %v, want = %v", got, want)
	}
}

func TestParseBody_MissingLines(t *testing.T) {
	const missing = `
I am foo@bar.com
My public key is
foo
`
	_, _, _, err := ParseBody(missing)
	expectedErr := errors.E(errors.Invalid, errors.Str("badly formatted email message"))
	if !errors.Match(expectedErr, err) {
		t.Errorf("err = %s, want = %s", err, expectedErr)
	}
}

func TestParseBody_HTMLAndEmptyLines(t *testing.T) {
	const funky = `
   I *am* bla@bleh.com   *
   	*My public key is *
   	p256*
   	          999*9*9
   102345***
     Signature:
1234*:*1432*5*324


	`

	userName, pubKey, sig, err := ParseBody(funky)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := userName, upspin.UserName("foo@bar.com"); got != want {
		t.Errorf("userName = %q, want = %q", got, want)
	}
	if got, want := pubKey, upspin.PublicKey("p256\n1063349993423423435345345345345345340\n3453453457828271720003453453245354698\n"); got != want {
		t.Errorf("pubKey = %q, want = %q", got, want)
	}
	var rs, ss big.Int
	if _, ok := rs.SetString("122323423", 10); !ok {
		t.Fatal("cannot parse R signature")
	}
	if _, ok := ss.SetString("199993211232983", 10); !ok {
		t.Fatal("cannot parse S signature")
	}
	expectedSig := upspin.Signature{
		R: &rs,
		S: &ss,
	}
	if got, want := sig, expectedSig; !reflect.DeepEqual(got, want) {
		t.Errorf("sig = %v, want = %v", got, want)
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
