// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package installer installs a new Upspin deployment for a domain.
package main

import (
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/upspin"
)

type server struct {
	mux      *http.ServeMux
	homeDir  string
	hostname string
	configs  []upspin.Config
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
	mux.HandleFunc("/", s.pickUser)
	mux.HandleFunc("/exit", s.exit)
	mux.HandleFunc("/setupdomain", s.setupDomain)
	mux.HandleFunc("/createuser", s.createUser)
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
	s.errorLinef(w, "Installer must be called from the same hostname (%s). Calling from %s", s.hostname, r.RemoteAddr)
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
<style>
input {
font-family: Droid Sans Mono;
}
</style>
</head>
<body>
<script src="https://cdnjs.cloudflare.com/ajax/libs/jquery/2.1.4/jquery.min.js"></script>
<script src="/assets/js/code.js" type="text/javascript"></script>
<div class="container">

<div class="row">
	<div class="col-md-4"><img src="https://upspin.io/images/augie.jpg" width="300"></div>
	<div class="col-md-4"><h1><font color="red">Installer, mate</font></h1></div>
	<div class="col-md-4"><img src="/assets/img/installermate.jpg" width="300"></div>
</div>
<br/>
`))

var footerTpl = template.Must(template.New("footer").Parse(`
<br/>
<div class="row">
	<div class="col-md-2"></div>
	<div class="col-md-8">
		<form method="get" id="next" action="{{.NextURL}}">
		{{range $key, $val := .Params}}
			<input type="hidden" name="{{$key}}" value="{{$val}}">
		{{end}}
		<button type="submit" class="btn btn-primary">{{.NextTxt}}</button>
		</form>
	</div>
	<div class="col-md-2"></div>
</div>

</div>  <!-- container -->
</body>
</html>
`))
