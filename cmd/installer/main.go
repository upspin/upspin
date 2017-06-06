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

var (
	port = flag.String("port", ":8080", "local port for listening")
)

func main() {
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
	mux.HandleFunc("/denied", s.deniedHandler)
	mux.HandleFunc("/", s.homeHandler)

	http.ListenAndServe(*port, s)
}

// ServeHTTP implements http.Handler. It wraps the mux with a layer that rejects
// and redirects non-local accesses, to prevent the installer from being
// remotely controlled by an attacker.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remote := hostname(r.RemoteAddr)
	fmt.Printf("remote: %s\n", remote)
	if remote != "localhost" && remote != s.hostname && remote != "127.0.0.1" && r.URL.Path != "/denied" {
		http.Redirect(w, r, "/denied", http.StatusTemporaryRedirect)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func hostname(hostport string) string {
	remote := hostport
	if c := strings.Index(remote, ":"); c >= 0 {
		remote = remote[:c]
	}
	return remote
}

func (s *server) deniedHandler(w http.ResponseWriter, r *http.Request) {
	remote := hostname(r.RemoteAddr)
	fmt.Fprintf(w, "Installer must be called from the same hostname (%s). Calling from %s", s.hostname, remote)
}

func (s *server) homeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Yo")
}
