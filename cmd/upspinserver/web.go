// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"upspin.io/client"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	// Load required transports
)

type web struct {
	cli upspin.Client
}

func newWeb(cfg upspin.Config) http.Handler {
	return &web{cli: client.New(cfg)}
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

	// If the parsed path differs from the requested path, redirect.
	name := p.Path()
	if name != urlName {
		http.Redirect(w, r, "/"+string(name), http.StatusFound)
		return
	}

	des, err := s.cli.Glob(string(name))
	if err != nil {
		http.Error(w, "Glob: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(des) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
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
				http.Error(w, "Glob: "+err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "Get: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(data)
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
