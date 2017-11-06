// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package keyserver

import (
	"reflect"
	"strings"
	"testing"

	"upspin.io/serverutil/signup"
)

func TestMailConfig(t *testing.T) {
	for _, tt := range []struct {
		name   string
		data   string
		mc     *signup.MailConfig
		errStr string
	}{
		{
			name: "ok",
			data: `
apikey: 123
project: test
notify: me@email.com
from: server@upspin.io`,
			mc: &signup.MailConfig{
				Project: "test",
				Notify:  "me@email.com",
				From:    "server@upspin.io",
			},
		},
		{
			name: "error-from",
			data: `
apikey: 123
project: test
notify: me@email.com`,
			errStr: `key "from" is missing`,
		},
		{
			name: "error-apikey",
			data: `
project: test
notify: me@email.com
from: server@upspin.io`,
			errStr: `key "apikey" is missing`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mc, err := parseMailConfig([]byte(tt.data))
			if tt.errStr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected an error")
				}
				if !strings.Contains(err.Error(), tt.errStr) {
					t.Fatalf(`expected error to contain "%s", but got: %s`, tt.errStr, err)
				}
			}
			if tt.mc != nil {
				if mc.Mail == nil {
					t.Fatal("expected Mail to be set")
				}
				if typ := reflect.TypeOf(mc.Mail).String(); typ != "*sendgrid.sendgrid" {
					t.Fatalf(`expected mail to be "*sendgrid.sendgrid", got: "%s"`, typ)
				}
				tt.mc.Mail = mc.Mail
				if !reflect.DeepEqual(tt.mc, mc) {
					t.Fatalf("expected %#v, got %#v", tt.mc, mc)
				}
			}
		})
	}
}
