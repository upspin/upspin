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
	homeDir  string
	hostname string
	cfg      upspin.Config
}

const deniedURL = "/denied"

func main() {
	port := flag.String("port", ":8080", "local port for listening")
	homeDir := flag.String("homedir", "", "location of home dir")

	flags.Parse(flags.Client)

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
		homeDir:  *homeDir,
		hostname: host,
	}
	mux.HandleFunc(deniedURL, s.denied)
	mux.HandleFunc("/", s.home)
	mux.HandleFunc("/exit", s.exit)
	mux.HandleFunc("/pickuser", s.pickUser)
	mux.Handle("/assets/", http.FileServer(http.Dir(".")))
	fmt.Printf("Installer running on localhost:%s\n", *port)
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

func (s *server) denied(w http.ResponseWriter, r *http.Request) {
	s.errorf(w, "Installer must be called from the same hostname (%s). Calling from %s", s.hostname, r.RemoteAddr)
}

func (s *server) home(w http.ResponseWriter, r *http.Request) {
	s.info(w, "Welcome", "Quit", "/exit")
}

func (s *server) exit(w http.ResponseWriter, r *http.Request) {
	fmt.Println("All done.")
	os.Exit(0)
}

var headerTpl = template.Must(template.New("header").Parse(`
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/assets/css/bootstrap.min.css">
<link rel="stylesheet" type="text/css" href="https://fonts.googleapis.com/css?family=Droid+Sans+Mono">
</head>
<body>
<div class="container">
<img src="https://upspin.io/images/augie.jpg">
{{range .Text}}
<div class="row">
<div class=".col-md-12">{{.}}</div>
</div>
{{end}}
`))

var footerTpl = template.Must(template.New("footer").Parse(`
<form method="get" action="{{.NextURL}}">
{{range $key, $val := .Params}}
<input type="hidden" name="{{$key}}" value="{{$val}}">
{{end}}
<button type="submit" class="btn btn-primary">{{.NextTxt}}</button>
</form>
</div>
</div>
</body>
</html>
`))
