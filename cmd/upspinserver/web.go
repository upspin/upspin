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

	"upspin.io/access"
	"upspin.io/client"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/serverutil/perm"
	"upspin.io/upspin"
)

type web struct {
	cfg upspin.Config
	cli upspin.Client
	perm *perm.Perm
}

func newWeb(cfg upspin.Config, perm *perm.Perm) http.Handler {
	if !*enableWeb {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return &web{
		cfg: cfg,
		cli: client.New(cfg),
		perm: perm,
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

	// If the parsed path differs from the requested path, redirect.
	name := p.Path()
	if name != urlName {
		http.Redirect(w, r, "/"+string(name), http.StatusFound)
		return
	}

	// Check user for the path is a Writer for the server.
	if !s.perm.IsWriter(p.User()) {
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
		return
	}

	// Lookup name.
	entry, err := s.cli.Lookup(name, true)
	switch {
	case errors.Match(errors.E(errors.NotExist), err):
		// Handle NotExist later, as response depends on
		// whether 'All' has 'list' right.
	case errors.Match(errors.E(errors.BrokenLink), err):
		// Can't follow link so response will be based on
		// containing directory Access.
		name = p.Drop(1).Path()
	case err != nil:
		httpError(w, err)
		return
	default:
		// Update name as we may have followed a link.
		name = entry.Name
	}

	// Get the Access file for the path.
	dir, err := s.cli.DirServer(name)
	if err != nil {
		httpError(w, err)
		return
	}
	whichAccess, err := dir.WhichAccess(name)
	if err != nil {
		httpError(w, err)
		return
	}
	// No Access file, default permission denied.
	if whichAccess == nil {
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
		return
	}
	accessData, err := clientutil.ReadAll(s.cfg, whichAccess)
	if err != nil {
		httpError(w, err)
		return
	}
	acc, err := access.Parse(whichAccess.Name, accessData)
	if err != nil {
		httpError(w, err)
		return
	}
	// Check read and list rights for AllUsers.
	readable, err := acc.Can(access.AllUsers, access.Read, name, s.cli.Get)
	if err != nil {
		httpError(w, err)
		return
	}
	listable, err := acc.Can(access.AllUsers, access.List, name, s.cli.Get)
	if err != nil {
		httpError(w, err)
		return
	}

	switch {
	case entry == nil && listable:
		code := http.StatusNotFound
		http.Error(w, http.StatusText(code), code)
	case entry == nil:
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
	case entry.IsDir() && listable:
		var d dirTemplateData
		d.Dir = p.Path()
		d.Content, err = s.cli.Glob(upspin.AllFilesGlob(name))
		if err != nil {
			httpError(w, err)
			return
		}
		if p.NElem() > 0 {
			d.Parent = p.Drop(1).Path()
		}
		if err := dirTemplate.Execute(w, d); err != nil {
			log.Error.Printf("rendering directory template: %v", err)
			httpError(w, err)
			return
		}
	case !entry.IsDir() && readable:
		data, err := s.cli.Get(name)
		if err != nil {
			httpError(w, err)
			return
		}
		w.Write(data)
	default:
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
	}
}

func httpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Match(errors.E(errors.Private), err),
		errors.Match(errors.E(errors.Permission), err):
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
	case errors.Match(errors.E(errors.NotExist), err),
		errors.Match(errors.E(errors.BrokenLink), err):
		code := http.StatusNotFound
		http.Error(w, http.StatusText(code), code)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type dirTemplateData struct {
	Dir  upspin.PathName
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
<h1>Index of {{.Dir}}</h1>
<ul>
{{with .Parent}}
	<li><a href="/{{.}}">../</a></li>
{{end}}
{{range .Content}}
	<li><a href="/{{$.Dir}}/{{shortname .Name}}">{{shortname .Name}}{{if .IsDir}}/{{end}}</a></li>
{{end}}
</ul>
`))
