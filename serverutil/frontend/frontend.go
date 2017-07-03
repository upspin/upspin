// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package frontend provides a web server that serves documentation and meta
// tags to instruct "go get" where to find the Upspin source repository.
package frontend // import "upspin.io/serverutil/frontend"

import (
	"bytes"
	"flag"
	"fmt"
	"go/build"
	"html/template"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NYTimes/gziphandler"
	"github.com/russross/blackfriday"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/serverutil/web"
	"upspin.io/upspin"

	_ "upspin.io/pack/eeintegrity"
	_ "upspin.io/transports"
)

var (
	docPath = flag.String("docpath", defaultDocPath(), "location of folder containing documentation")
)

func Main() {
	flags.Parse(flags.Server)

	if err := parseTemplates(filepath.Join(*docPath, "templates")); err != nil {
		log.Fatalf("error parsing templates: %v", err)
	}

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", newServer(cfg))

	if !flags.InsecureHTTP {
		go func() {
			log.Printf("Serving HTTP->HTTPS redirect on %q", flags.HTTPAddr)
			log.Fatal(http.ListenAndServe(flags.HTTPAddr, http.HandlerFunc(redirectHTTP)))
		}()
	}
}

var baseTmpl, docTmpl, doclistTmpl, downloadTmpl *template.Template

func parseTemplates(dir string) (err error) {
	baseTmpl, err = template.ParseFiles(filepath.Join(dir, "base.tmpl"))
	if err != nil {
		return err
	}
	docTmpl, err = template.ParseFiles(filepath.Join(dir, "base.tmpl"), filepath.Join(dir, "doc.tmpl"))
	if err != nil {
		return err
	}
	doclistTmpl, err = template.ParseFiles(filepath.Join(dir, "base.tmpl"), filepath.Join(dir, "doclist.tmpl"))
	if err != nil {
		return err
	}
	downloadTmpl, err = template.ParseFiles(filepath.Join(dir, "base.tmpl"), filepath.Join(dir, "download.tmpl"))
	return err
}

const (
	extMarkdown  = ".md"
	docHostname  = "upspin.io"      // redirect doc requests to this host
	testHostname = "test.upspin.io" // don't redirect requests to this host
)

// sourceRepo is a map from each custom domain to their repo base URLs.
var sourceRepo = map[string]string{
	"upspin.io": "https://upspin.googlesource.com/upspin",

	"android.upspin.io": "https://upspin.googlesource.com/android",
	"augie.upspin.io":   "https://upspin.googlesource.com/augie",
	"aws.upspin.io":     "https://upspin.googlesource.com/aws",
	"exp.upspin.io":     "https://upspin.googlesource.com/exp",
	"gcp.upspin.io":     "https://upspin.googlesource.com/gcp",
}

func defaultDocPath() string {
	p, err := build.Import("upspin.io/doc", "", build.FindOnly)
	if err != nil {
		return ""
	}
	return p.Dir
}

func redirectHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS != nil || r.Host == "" {
		http.NotFound(w, r)
		return
	}

	u := r.URL
	u.Host = r.Host
	u.Scheme = "https"
	http.Redirect(w, r, u.String(), http.StatusFound)
}

type server struct {
	handlers http.Handler // stack of wrapped http.Handlers
	mux      *http.ServeMux
	docList  []string
	docHTML  map[string][]byte
	docTitle map[string]string
}

// newServer initializes and returns a new HTTP server.
func newServer(cfg upspin.Config) http.Handler {
	s := &server{mux: http.NewServeMux()}
	s.handlers = goGetHandler{gziphandler.GzipHandler(canonicalHostHandler{s.mux})}
	s.mux.Handle("/", http.HandlerFunc(s.handleRoot))
	s.mux.Handle("/doc/", http.HandlerFunc(s.handleDoc))
	s.mux.Handle("/images/", http.FileServer(http.Dir(*docPath)))
	if cfg != nil {
		s.mux.Handle(downloadPath, newDownloadHandler(cfg))
		s.mux.Handle("/"+releaseUser+"/", web.New(cfg, isWriter(releaseUser)))
	}

	if err := s.parseDocs(*docPath); err != nil {
		log.Error.Fatalf("Could not parse docs in %s: %s", *docPath, err)
	}
	return s
}

// isWriter is a serverutil/web.IsWriter implementation.
type isWriter upspin.UserName

// IsWriter reports whether the given user matches isWriter.
func (w isWriter) IsWriter(u upspin.UserName) bool {
	return upspin.UserName(w) == u
}

type pageData struct {
	Title   string
	Content interface{}
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Path != "/" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	s.renderDoc(w, "index.md")
}

func (s *server) handleDoc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Path == "/doc/" {
		d := pageData{Content: struct {
			List  []string
			Title map[string]string
		}{s.docList, s.docTitle}}
		if err := doclistTmpl.Execute(w, d); err != nil {
			log.Error.Printf("Error executing root content template: %s", err)
		}
		return
	}
	s.renderDoc(w, filepath.Base(r.URL.Path))
}

func (s *server) renderDoc(w http.ResponseWriter, fn string) {
	b, ok := s.docHTML[fn]
	if !ok {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	if err := docTmpl.Execute(w, pageData{
		Title:   s.docTitle[fn] + " · Upspin",
		Content: template.HTML(b),
	}); err != nil {
		log.Error.Printf("Error executing doc content template: %s", err)
		return
	}
}

// ServeHTTP satisfies the http.Handler interface for a server.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS != nil {
		w.Header().Set("Strict-Transport-Security", "max-age=86400; includeSubDomains")
	}
	s.handlers.ServeHTTP(w, r)
}

func (s *server) parseDocs(path string) error {
	fis, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	var (
		html  = map[string][]byte{}
		title = map[string]string{}
		list  = []string{}
	)
	for _, fi := range fis {
		fn := fi.Name()
		if filepath.Ext(fn) != extMarkdown {
			continue
		}
		b, err := ioutil.ReadFile(filepath.Join(path, fn))
		if err != nil {
			return err
		}
		html[fn] = blackfriday.MarkdownCommon(b)
		title[fn] = docTitle(b)
		list = append(list, fn)
	}
	s.docHTML = html
	s.docTitle = title
	s.docList = list
	sort.Strings(s.docList)
	return nil
}

// docTitle extracts the first Markdown header in the given document body.
// It expects the first line to be of the form
// 	# Title string
// If not, it will return "Untitled".
func docTitle(b []byte) string {
	if len(b) > 2 && b[0] == '#' {
		if i := bytes.IndexByte(b, '\n'); i != -1 {
			// On Windows we need to strip out the '\r' as well
			if b[i-1] == '\r' {
				i--
			}
			return string(b[2:i])
		}
	}
	return "Untitled"
}

type goGetHandler struct {
	http.Handler
}

func (h goGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("go-get") == "1" {
		base := r.Host
		repo, ok := sourceRepo[base]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, base, repo)
		return
	}
	h.Handler.ServeHTTP(w, r)
}

type canonicalHostHandler struct {
	http.Handler
}

func (h canonicalHostHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Redirect requests to foo.upspin.io to upspin.io.
	if r.Host != docHostname && r.Host != testHostname && strings.HasSuffix(r.Host, "."+docHostname) {
		u := *r.URL
		u.Host = docHostname
		http.Redirect(w, r, u.String(), http.StatusFound)
		return
	}
	h.Handler.ServeHTTP(w, r)
}
