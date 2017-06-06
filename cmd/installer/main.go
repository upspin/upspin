// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package installer installs a new Upspin deployment for a domain.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"html/template"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/transports"
	"upspin.io/upspin"
)

type server struct {
	mux      *http.ServeMux
	userDir  string
	hostname string
	cfg      upspin.Config
}

const deniedURL = "/denied"

func main() {
	port := flag.String("port", ":8080", "local port for listening")
	homeDir := flag.String("homedir", "", "location of home dir")

	flags.Parse(flags.Client)

	// Is there a user already?
	cfg, _ := config.FromFile(flags.Config) // errors ok; dealt with later.
	transports.Init(cfg)

	host, _ := os.Hostname() // errors ok; dealt with later.

	if *homeDir == "" {
		userDir, err := config.Homedir()
		if err != nil {
			fmt.Fprint(os.Stderr, "No home dir found, use -homedir")
			os.Exit(1)
		}
		*homeDir = userDir
	}

	mux := http.NewServeMux()
	s := &server{
		mux:      mux,
		userDir:  *homeDir,
		hostname: host,
	}
	mux.HandleFunc(deniedURL, s.deniedHandler)
	mux.HandleFunc("/", s.homeHandler)
	mux.HandleFunc("/prepare", s.prepareHandler)
	mux.HandleFunc("/run", s.runHandler)
	mux.Handle("/assets/", http.FileServer(http.Dir(".")))
	http.ListenAndServe(*port, s)
}

// ServeHTTP implements http.Handler. It wraps the mux with a layer that rejects
// and redirects non-local accesses, to prevent the installer from being
// remotely controlled by an attacker.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remote := hostname(r.RemoteAddr)
	fmt.Printf("remote: %s\n", remote)
	if remote != "localhost" && remote != s.hostname && remote != "127.0.0.1" &&
		remote != "[::1]" && r.URL.Path != deniedURL {
		http.Redirect(w, r, deniedURL, http.StatusTemporaryRedirect)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func hostname(hostport string) string {
	remote := hostport
	if c := strings.LastIndex(remote, ":"); c >= 0 {
		remote = remote[:c]
	}
	return remote
}

func (s *server) deniedHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Installer must be called from the same hostname (%s). Calling from %s", s.hostname, r.RemoteAddr)
}

func (s *server) homeHandler(w http.ResponseWriter, r *http.Request) {
	headerTpl.Execute(w, "")
	footerTpl.Execute(w, "")
}

// prepareHandler renders HTML according to the specifications in the URL
// parameters, preparing for a command run, which will ultimately invoke
// s.runHandler.
func (s *server) prepareHandler(w http.ResponseWriter, r *http.Request) {

}

// runHandler is an AJAX endpoint that runs a given command and sends out the
// results as raw text, capturing both stderr and stdout. It's essentially a
// remote, web-y version of exec.Command.
func (s *server) runHandler(w http.ResponseWriter, r *http.Request) {

}

var headerTpl = template.Must(template.New("header").Parse(`
<!DOCTYPE html>
<html lang="en">
<head>
<link rel="stylesheet" href="/assets/css/bootstrap.min.css">
<link rel="stylesheet" type="text/css" href="https://fonts.googleapis.com/css?family=Droid+Sans+Mono">
</head>
<body>
<img src="https://upspin.io/images/augie.jpg">
<button type="button" class="btn btn-primary">Primary</button>
`))

var footerTpl = template.Must(template.New("footer").Parse(`
</body>
</html>
`))

var tpl = `
<html>
<body>
Yo!
<img src="https://upspin.io/images/augie.jpg">
<div>
Going to run: echo "hello world"
</div>
<button type="button" class="btn btn-primary">Primary</button>
</body>
</html>
`
