// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A web server that serves "hello world" and meta tags to instruct "go get"
// where to find the upspin source repository.
package main

import (
	"flag"
	"fmt"
	"net/http"

	"upspin.io/cloud/https"
)

var httpsAddr = flag.String("https_addr", "localhost:8000", "HTTPS listen address")

func main() {
	flag.Parse()
	http.HandleFunc("/", handler)
	https.ListenAndServe("frontend", *httpsAddr, nil)
}

const (
	sourceBase = "upspin.io"
	sourceRepo = "https://upspin.googlesource.com/upspin"
)

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("go-get") == "1" {
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
		return
	}
	w.Write([]byte("Hello, upspin"))
}
