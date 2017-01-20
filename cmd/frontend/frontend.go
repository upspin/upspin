// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A web server that serves documentation and meta tags to instruct "go get"
// where to find the upspin source repository.
package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/russross/blackfriday"
	"upspin.io/cloud/https"
	"upspin.io/flags"
	"upspin.io/log"
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

var docBasePath = flag.String("docpath", "../../doc", "location of folder containing documentation")

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
	s.mux.HandleFunc("/doc/", s.handleDoc)
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("go-get") == "1" {
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
		return
	}
	t, err := template.ParseFiles("templates/base.tmpl")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := t.Execute(w, nil); err != nil {
		log.Error.Printf("Error executing root content template: %s", err)
		return
	}
}

func (s *server) handleDoc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	filename := filepath.Base(r.URL.Path) + ".md"
	f, err := os.Open(filepath.Join(*docBasePath, filename))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	defer f.Close()
	b, err := ioutil.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	output := blackfriday.MarkdownCommon(b)
	baseTmpl, err := template.ParseFiles("templates/base.tmpl")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	contentTmpl, err := template.Must(baseTmpl.Clone()).Parse(`{{define "content"}}{{.}}{{end}}`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := contentTmpl.Execute(w, template.HTML(output)); err != nil {
		log.Error.Printf("Error executing doc content template: %s", err)
		return
	}
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
