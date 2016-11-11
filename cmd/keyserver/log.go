// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"net/http"

	"upspin.io/log"
	"upspin.io/upspin"
)

type logHandler struct {
	key upspin.KeyServer
}

func (h logHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _, err := h.key.Log(0)
	if err != nil {
		log.Error.Printf("logHandler: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}
