// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg,andybons): make the HTML pages prettier

package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"upspin.io/client"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

type web struct {
	cfg upspin.Config
	cli upspin.Client
}

func newWeb(cfg upspin.Config) http.Handler {
	return &web{
		cfg: cfg,
		cli: client.New(cfg),
	}
}

func (s *web) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		fmt.Fprintln(w, "Hello, upspin")
		return
	}

	urlName := upspin.PathName(strings.TrimPrefix(r.URL.Path, "/"))
	p, err := path.Parse(urlName)
	if err != nil {
		http.Error(w, "Parse: "+err.Error(), http.StatusBadRequest)
		return
	}

	// The server user can always see its own tree,
	// so prevent access entirely from the web interface.
	if p.User() == s.cfg.UserName() {
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
		return
	}

	// If the parsed path differs from the requested path, redirect.
	name := p.Path()
	if name != urlName {
		http.Redirect(w, r, "/"+string(name), http.StatusFound)
		return
	}

	des, err := s.cli.Glob(string(name))
	if err != nil {
		httpError(w, err)
		return
	}
	if len(des) == 0 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if len(des) > 1 || des[0].IsDir() {
		// Display glob listing or directory contents.
		var d dirTemplateData
		if len(des) > 1 {
			d.Glob = name
			d.Content = des
		} else {
			d.Dir = des[0]
			d.Content, err = s.cli.Glob(string(name) + "/*")
			if err != nil {
				httpError(w, err)
				return
			}
			if p.NElem() > 0 {
				d.Parent = p.Drop(1).Path()
			}
		}
		if err := dirTemplate.Execute(w, d); err != nil {
			log.Error.Printf("rendering directory template: %v", err)
		}
		return
	}

	// Serve the file.
	data, err := s.cli.Get(name)
	if err != nil {
		httpError(w, err)
		return
	}
	w.Write(data)
}

func httpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Match(errors.E(errors.Private), err),
		errors.Match(errors.E(errors.Permission), err):
		http.Error(w, "Forbidden", http.StatusForbidden)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type dirTemplateData struct {
	// One and only one of these must be set.
	Dir  *upspin.DirEntry
	Glob upspin.PathName

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

var dirTemplate = template.Must(template.New("dir").Funcs(templateFuncs).Parse(`
{{with .Dir}}
<h1>Index of {{.Name}}</h1>
{{end}}
{{with .Glob}}
<h1>Matches for {{.}}</h1>
{{end}}
<ul>
{{with .Parent}}
	<li><a href="/{{.}}">../</a></li>
{{end}}
{{range .Content}}
	<li><a href="/{{.Name}}">{{shortname .Name}}{{if .IsDir}}/{{end}}</a></li>
{{end}}
</ul>
`))
