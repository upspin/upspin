// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A web server that serves "hello world" and meta tags to instruct "go get"
// where to find the upspin source repository.
package main

import (
	"fmt"
	"net/http"
	"strings"

	"upspin.io/cloud/https"
	"upspin.io/flags"
)

func main() {
	flags.Parse("https", "letscache", "log", "tls")
	http.Handle("/", newServer())
	https.ListenAndServeFromFlags(nil, "frontend")
}

const (
	sourceBase = "upspin.io"
	sourceRepo = "https://upspin.googlesource.com/upspin"
)

type server struct {
	mux *http.ServeMux
}

// newServer allocates and returns a new server.
func newServer() http.Handler {
	s := &server{mux: http.NewServeMux()}
	s.init()
	return s
}

// init sets up a server by performing tasks like mapping path endpoints to
// handler functions.
func (s *server) init() {
	s.mux.HandleFunc("/", s.handleRoot)
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("go-get") == "1" {
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
		return
	}
	w.Write([]byte("Hello, upspin"))
}

// ServeHTTP satisfies the http.Handler interface for a server. It
// will compress all responses if the appropriate request headers are set.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		s.mux.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	gzw := newGzipResponseWriter(w)
	defer gzw.Close()
	s.mux.ServeHTTP(gzw, r)
}
