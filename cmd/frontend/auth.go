// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// This file implements a wrapping http.Handler that
// implements HTTP Basic Access Authentication.
// It lets us deploy the docs without publishing them to unauthorized people.
// TODO(adg): delete before launch.

import (
	"encoding/base64"
	"net/http"
	"strings"
)

type basicAuthHandler struct {
	Username string
	Password string
	Handler  http.Handler
}

func (h *basicAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.valid(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Upspin"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	h.Handler.ServeHTTP(w, r)
}

func (h *basicAuthHandler) valid(r *http.Request) bool {
	s := r.Header.Get("Authorization")
	const prefix = "Basic "
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	s = s[len(prefix):]
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return false
	}
	p := strings.SplitN(string(b), ":", 2)
	return len(p) == 2 && p[0] == h.Username && p[1] == h.Password
}
