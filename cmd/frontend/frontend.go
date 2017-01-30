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
	"sort"
	"strings"

	"github.com/russross/blackfriday"

	"upspin.io/cloud/https"
	"upspin.io/flags"
	"upspin.io/log"
)

var (
	docPath = flag.String("docpath", defaultDocPath(), "location of folder containing documentation")
)

func main() {
	flags.Parse("http", "https", "letscache", "log", "tls")
	http.Handle("/", newServer())
	go http.ListenAndServe(flags.HTTPAddr, redirectToHTTPSHandler())
	https.ListenAndServeFromFlags(nil, "frontend")
}

const (
	sourceBase = "upspin.io"
	sourceRepo = "https://upspin.googlesource.com/upspin"

	extMarkdown = ".md"

	// TODO(adg): remove the auth check before launch
	username = "upspin"
	password = "cheesemaster"
)

var (
	baseTmpl    = template.Must(template.ParseFiles("templates/base.tmpl"))
	docTmpl     = template.Must(template.ParseFiles("templates/base.tmpl", "templates/doc.tmpl"))
	doclistTmpl = template.Must(template.ParseFiles("templates/base.tmpl", "templates/doclist.tmpl"))
)

func defaultDocPath() string {
	return filepath.Join(os.Getenv("GOPATH"), "src/upspin.io/doc")
}

func redirectToHTTPSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+flags.HTTPSAddr+r.RequestURI, http.StatusTemporaryRedirect)
	})
}

type server struct {
	mux          *http.ServeMux
	doclist      []string
	renderedDocs map[string][]byte
}

// newServer allocates and returns a new HTTP server.
func newServer() http.Handler {
	s := &server{mux: http.NewServeMux()}
	s.init()
	return s
}

// init sets up a server by performing tasks like mapping path endpoints to
// handler functions.
func (s *server) init() {
	if err := s.parseDocs(*docPath); err != nil {
		log.Error.Fatalf("Could not parse docs in %s: %s", *docPath, err)
	}

	s.mux.Handle("/", goGetHandler{&basicAuthHandler{
		Username: username,
		Password: password,
		Handler:  http.HandlerFunc(s.handleRoot),
	}})
	s.mux.Handle("/doc/", &basicAuthHandler{
		Username: username,
		Password: password,
		Handler:  http.HandlerFunc(s.handleDoc),
	})
	s.mux.Handle("/images/", http.FileServer(http.Dir("./")))
}

type pageData struct {
	Title   string
	Content interface{}
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("go-get") == "1" {
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
		return
	}
	if r.URL.Path != "/" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	s.renderDoc(w, "index.md")
}

func (s *server) handleDoc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Path == "/doc/" {
		if err := doclistTmpl.Execute(w, pageData{Content: s.doclist}); err != nil {
			log.Error.Printf("Error executing root content template: %s", err)
		}
		return
	}
	s.renderDoc(w, filepath.Base(r.URL.Path))
}

func (s *server) renderDoc(w http.ResponseWriter, fn string) {
	b, ok := s.renderedDocs[fn]
	if !ok {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if err := docTmpl.Execute(w, pageData{
		Title:   fn + " · Upspin",
		Content: template.HTML(b),
	}); err != nil {
		log.Error.Printf("Error executing doc content template: %s", err)
		return
	}
}

// ServeHTTP satisfies the http.Handler interface for a server. It
// will compress all responses if the appropriate request headers are set.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Strict-Transport-Security", "max-age=86400; includeSubDomains")

	if r.URL.Path == "/favicon.ico" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		s.mux.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	gzw := newGzipResponseWriter(w)
	defer gzw.Close()
	s.mux.ServeHTTP(gzw, r)
}

func (s *server) parseDocs(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}

	fis, err := f.Readdir(0)
	if err != nil {
		return err
	}
	rendered := map[string][]byte{}
	doclist := []string{}
	for _, fi := range fis {
		if filepath.Ext(fi.Name()) != extMarkdown {
			continue
		}
		f, err := os.Open(filepath.Join(path, fi.Name()))
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := ioutil.ReadAll(f)
		if err != nil {
			return err
		}
		doclist = append(doclist, fi.Name())
		rendered[fi.Name()] = blackfriday.MarkdownCommon(b)
	}
	s.renderedDocs = rendered
	sort.Strings(doclist)
	s.doclist = doclist
	return nil
}

type goGetHandler struct {
	Handler http.Handler
}

func (h goGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("go-get") == "1" {
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
		return
	}
	h.Handler.ServeHTTP(w, r)
}
