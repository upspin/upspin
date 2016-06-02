// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A simple static file server serving on port 443 with SSL with a redirector
// on port 80 to 443. It also serves meta tags to instruct "go get" where to
// find the upspin source repository.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

var (
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

func main() {
	go func() {
		log.Fatal(http.ListenAndServe(":80", http.RedirectHandler("https://upspin.io", http.StatusMovedPermanently)))
	}()
	http.HandleFunc("/", handler)
	log.Fatal(http.ListenAndServeTLS(":443", *sslCertificateFile, *sslCertificateKeyFile, nil))
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
	http.FileServer(http.Dir("/var/www/public_root")).ServeHTTP(w, r)
}
