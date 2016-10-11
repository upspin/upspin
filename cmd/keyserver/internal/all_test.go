// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package internal

import (
	"bytes"
	"net/http"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

func TestParseMail(t *testing.T) {
	req := newMockRequest(t, dataPassesDKIM)
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

func TestParseMailFailDKIM(t *testing.T) {
	req := newMockRequest(t, dataFailsDKIM)
	_, _, err := ParseMail(req.Form)
	expectedErr := errors.E(errors.Permission)
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

func newMockRequest(t *testing.T, data string) *http.Request {
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

const dataFailsDKIM = `--xYzZY
Content-Disposition: form-data; name="headers"

Received: by mx0037p1mdw1.sendgrid.net with SMTP id 95QpD0sSF3 Tue, 11 Oct 2016 21:00:25 +0000 (UTC)
Received: from mail-yb0-f182.google.com (mail-yb0-f182.google.com [209.85.213.182]) by mx0037p1mdw1.sendgrid.net (Postfix) with ESMTPS id 98736603B47 for <foo@key.upspin.io>; Tue, 11 Oct 2016 21:00:25 +0000 (UTC)
Received: by mail-yb0-f182.google.com with SMTP id 191so12787716ybv.3 for <foo@key.upspin.io>; Tue, 11 Oct 2016 14:00:25 -0700 (PDT)
DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=gmail.com; s=20120113; h=mime-version:sender:from:date:message-id:subject:to; bh=oH36YwySLrV+cdCGdL8XI0SyO8VRtJOFWvo9I/9u3vo=; b=UmfM+BRQToF0z7iKzfhaNppVs4/b2bO6N2WeyB/XtGd+cpytz/rRhCpzkunhq6AO7z MunosxQ7SlC5oev+47R2e+Sk1Di+XqVkvXyBbMxvi9hBEolekP9Y5GMRZoR/wZo7QQLf ndMWuSGel6e1sGu9UhgahOY2panuWNIAqnvjKuOTjDxbVGR4Fi13jO77N0iOQIVJ7/G0 iQ+EVSfcUqFbyKzLIW19mnlMW5QF93y66z9fAjAEEFD/b4PvEX+h1qB7FtiEQMqbNjMy a++LBFlOED5nxAb9YlvszyiSPhqNZbYTAhdm401kFT6Vh5rxKW9a55DBD4TC0JU/Ua1r 5IxA==
X-Google-DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=1e100.net; s=20130820; h=x-gm-message-state:mime-version:sender:from:date:message-id:subject :to; bh=oH36YwySLrV+cdCGdL8XI0SyO8VRtJOFWvo9I/9u3vo=; b=dmr7zIaT1J9OisfqSjsMSjerzYCmU/Jpk16vufokNAg1xMi3L0JBUG1dfZ1lVOEIEC MplaIDbIHpYSZPPYTEMbl6ESFNMe17iy8PnFE7/5t/O2e5MxtaS7NGSj2E/SZHQO+tR+ 4PsMs4n4bkJiHQMPH5DIQ8Jhp+VZRKTK2BMPL9+YQQGj6atQync7bYBQX4fKMMgOhd7P k1yS539vYiePhCMSMM7N3ADwz+rwF1EqbJHg25GSq6Eo+GOkh741s7xZILwT0vwIM6OL aNdiy+5JyCTtoBFaiWtbul6ZKIGVQ3tfcg1ovqkOv+bfwezlGbmiWeoiVQHG4Z/PTMcz ykcg==
X-Gm-Message-State: AA6/9RkOatUIKJW+CpYJQsDDGJSXrKMv+ngjEmaxRctrWrwYqLVgbkruLe+yKJ2rbxyVFMnikzzxQFTIh5Q6OA==
X-Received: by 10.37.221.194 with SMTP id u185mr5427749ybg.135.1476219625233; Tue, 11 Oct 2016 14:00:25 -0700 (PDT)
MIME-Version: 1.0
Sender: zardoz@gmail.com
Received: by 10.13.226.132 with HTTP; Tue, 11 Oct 2016 14:00:24 -0700 (PDT)
From: Zardoz <zardoz@closedmind.org>
Date: Tue, 11 Oct 2016 14:00:24 -0700
X-Google-Sender-Auth: WEhUOGN81wSEK9-Ml3RYS_gQkZg
Message-ID: <CAC_Z_pQ5tkUGRy=DkgaSfzEnrvLg3HCJ7Muk9=V-izYyh1U4=A@mail.gmail.com>
Subject: the rain in spain
To: foo@key.upspin.io
Content-Type: multipart/alternative; boundary=001a114bca225f62aa053e9d2ac1

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

Zardoz <zardoz@closedmind.org>
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

const dataPassesDKIM = `--xYzZY
Content-Disposition: form-data; name="headers"

Received: by mx0030p1mdw1.sendgrid.net with SMTP id aeqSw23HLs Tue, 11 Oct 2016 20:45:33 +0000 (UTC)
Received: from mail-yb0-f178.google.com (mail-yb0-f178.google.com [209.85.213.178]) by mx0030p1mdw1.sendgrid.net (Postfix) with ESMTPS id 73261B48072 for <foo@key.upspin.io>; Tue, 11 Oct 2016 20:45:33 +0000 (UTC)
Received: by mail-yb0-f178.google.com with SMTP id 191so12659762ybv.3 for <foo@key.upspin.io>; Tue, 11 Oct 2016 13:45:33 -0700 (PDT)
DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=gmail.com; s=20120113; h=mime-version:from:date:message-id:subject:to; bh=Xli6lhAlqPl0hi/Hd2U6EZsSyB241v5ZNPO5/Iky0u8=; b=Yps5qU+7SqlyVoOwXlTSUQO6jxW4Y4mhJOcnmCdBhCbXmBqLzduRNPjTscpYC6s6kP T+HGcFXPzxyy+yDWw9/rhRSf4lCLMAkfYKwnVWm3K1GimB57UW6pbKLEDBq6/2GKZjcL tgCIawI2w8n/FLtqiP36jUUJWP0WH26OQ6cwBbua8SbZiOLU1cqsi4crnv3R9ddy2AFL N7zU1hCux04BCOKUXtUkdJUiV5X/pBpAVX+oNKzKMDGBuqYsz7N9fqHWKO5oQdwoBm3j AqzNST4IXsBCSJkQQLXiBIxVWhueXyR+5yptkHgT5htveffY9mGUrlvMMKQTDNY/4/Fx 2dQA==
X-Google-DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed; d=1e100.net; s=20130820; h=x-gm-message-state:mime-version:from:date:message-id:subject:to; bh=Xli6lhAlqPl0hi/Hd2U6EZsSyB241v5ZNPO5/Iky0u8=; b=UNZ7MoCk00PEp1GLi1KJtg9By9SYWf8quSG/sgVQye8yHJlbkccz+m2ba+mIGeKIxk AFhN/P0+ljkLoDnlEmBXdBzNl+t8KKc02oid0xGtHace3BpCU8cNCSUA7C6OeegeVTAP 1jOM7KtaOhgDeXdd8k86ujEWMaWrFbzLFK/sJ81Y2UPyixrGGTvFeDmbAmOFx1mh2l4z pkTkm98SCLrlnbu9/t+jAu+9YnitocdnINHKuhkN6wGPeVlu6SJr8gwq0hRt//1SxdZu VoRsAb9DLzyMQd45UeiBPNL6RjaPYKiDwH3CrRAzwSszXON762++stc11LXHv4YZfpsB hbpA==
X-Gm-Message-State: AA6/9Rn1AjEpRgZidzn3DYBCrHGKnmSfklDqCzOiMyujIi9ouvjTh5plNOhurgXExzTV06369DRPKn60FdirUA==
X-Received: by 10.37.108.66 with SMTP id h63mr5171224ybc.50.1476218732960; Tue, 11 Oct 2016 13:45:32 -0700 (PDT)
MIME-Version: 1.0
Received: by 10.13.227.130 with HTTP; Tue, 11 Oct 2016 13:45:32 -0700 (PDT)
From: Some Dude <dude@gmail.com>
Date: Tue, 11 Oct 2016 13:45:32 -0700
Message-ID: <CAKfjmvpGRxoXb0AaexbCaCZJh9w41EBw=Z1EmUJQhSprT=Sg1Q@mail.gmail.com>
Subject: subject!
To: foo@key.upspin.io
Content-Type: multipart/alternative; boundary=001a1148b4b23064ac053e9cf572

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
