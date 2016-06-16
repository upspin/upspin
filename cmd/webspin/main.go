// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command webspin is an HTTP frontend that serves upspin content
// accessible to the current user (as defined by their upspin/rc).
package main

import (
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"

	// TODO(adg): which of the the following _ imports are necessary?
	// This list was cribbed from upspin.io/cmd/upspin/main.go.

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/directory/transports"
	_ "upspin.io/store/transports"
	_ "upspin.io/user/transports"
)

var httpAddr = flag.String("http", "localhost:8080", "HTTP listen address")

func main() {
	flag.Parse()
	s := &Server{cli: newClient()}
	http.Handle("/", s)
	log.Fatal(http.ListenAndServe(*httpAddr, nil))
}

func newClient() upspin.Client {
	ctx, err := context.InitContext(nil)
	if err != nil {
		log.Fatal(err)
	}
	return client.New(ctx)
}

type Server struct {
	cli upspin.Client
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO(adg): appropriate HTTP errors

	if r.URL.Path == "/" {
		fmt.Fprintln(w, "Hello")
		return
	}

	urlName := upspin.PathName(strings.TrimPrefix(r.URL.Path, "/"))
	p, err := path.Parse(urlName)
	if err != nil {
		http.Error(w, "Parse: "+err.Error(), 500)
		return
	}

	// If the parsed path differs from the requested path, redirect.
	name := p.Path()
	if name != urlName {
		http.Redirect(w, r, "/"+string(name), http.StatusFound)
		return
	}

	dir, err := s.cli.Directory(name)
	if err != nil {
		http.Error(w, "Directory: "+err.Error(), 500)
		return
	}
	entry, err := dir.Lookup(name)
	if err != nil {
		http.Error(w, "Lookup: "+err.Error(), 500)
		return
	}

	if entry.IsDir() {
		// Display directory listing.
		des, err := s.cli.Glob(string(name) + "/*")
		if err != nil {
			http.Error(w, "Lookup: "+err.Error(), 500)
			return
		}
		var parent upspin.PathName
		if p.NElem() > 0 {
			parent = p.Drop(1).Path()
		}
		if err := dirTemplate.Execute(w, dirTemplateData{
			Dir:     entry,
			Parent:  parent,
			Content: des,
		}); err != nil {
			log.Error.Printf("rendering directory template: %v", err)
		}
		return
	}

	// Serve the file.
	data, err := s.cli.Get(name)
	if err != nil {
		http.Error(w, "Get: "+err.Error(), 500)
		return
	}
	w.Write(data)
}

type dirTemplateData struct {
	Dir     *upspin.DirEntry
	Parent  upspin.PathName
	Content []*upspin.DirEntry
}

var templateFuncs = template.FuncMap{
	"shortname": func(name upspin.PathName) upspin.PathName {
		parent := path.DropPath(name, 1)
		if name == parent {
			return name
		}
		if !strings.HasSuffix(string(parent), "/") {
			parent += "/"
		}
		return upspin.PathName(strings.TrimPrefix(string(name), string(parent)))
	},
}

var dirTemplate = template.Must(template.New("").Funcs(templateFuncs).Parse(`
<h1>Index of {{.Dir.Name}}</h1>
<ul>
{{with .Parent}}
	<li><a href="/{{.}}">../</a></li>
{{end}}
{{range .Content}}
	<li><a href="/{{.Name}}">{{shortname .Name}}{{if .IsDir}}/{{end}}</a></li>
{{end}}
</ul>
`))
