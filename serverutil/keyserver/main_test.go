// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package keyserver

import (
	"strings"
	"testing"
)

func TestMailConfig(t *testing.T) {
	for _, tt := range []struct {
		name   string
		data   string
		errStr string
	}{
		{
			name: "ok",
			data: `
apikey: 123
project: test
notify: me@email.com
from: server@upspin.io`,
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
			_, err := parseMailConfig([]byte(tt.data))
			if tt.errStr == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.errStr) {
				t.Fatalf(`expected error to contain "%s", but got: %s`, tt.errStr, err)
			}
		})
	}
}
