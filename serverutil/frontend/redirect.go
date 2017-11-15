// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package frontend

import (
	"net/http"
	"strconv"
	"strings"
)

// redirectHandler redirects requests from prefix/something to target/something.
// It rejects non-empty values of "something" that don't parse as int.
func redirectHandler(prefix, target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suffix := strings.TrimPrefix(r.URL.Path, prefix)
		if suffix != "" {
			if _, err := strconv.Atoi(suffix); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		http.Redirect(w, r, target+suffix, http.StatusFound)
	})
}
