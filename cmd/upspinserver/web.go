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
	"upspin.io/upspin"
)

type web struct {
	cfg upspin.Config
	cli upspin.Client
}

func newWeb(cfg upspin.Config) http.Handler {
	if !*enableWeb {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
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

	// If the parsed path differs from the requested path, redirect.
	name := p.Path()
	if name != urlName {
		http.Redirect(w, r, "/"+string(name), http.StatusFound)
		return
	}

	// Get the Access file for the path.
	whichAccess, err := s.whichAccessFollowLinks(name)
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
	// Fetch 'All's read and list rights.
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

	if !readable && !listable {
		// No point carrying on from here.
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
		return
	}

	// Try to serve the file.
	entry, err := s.cli.Lookup(name, true)
	if err == nil && entry.IsDir() {
		// Directory found but does 'all' have right to read.
		if !listable {
			code := http.StatusForbidden
			http.Error(w, http.StatusText(code), code)
			return
		}
		var d dirTemplateData
		d.Dir = name
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
		}
		return
	} else if err == nil {
		// File exisits, but do we have right to read.
		if !readable {
			code := http.StatusForbidden
			http.Error(w, http.StatusText(code), code)
			return
		}
		data, err := s.cli.Get(name)
		if err != nil {
			httpError(w, err)
			return
		}
		w.Write(data)
		return
	} else if errors.Match(errors.E(errors.NotExist), err) {
		if !listable {
			// 'all' have right to 'list' so should return not found...
			code := http.StatusForbidden
			http.Error(w, http.StatusText(code), code)
			return
		}
		entries, err := s.cli.Glob(string(name))
		if err != nil {
			httpError(w, err)
			return
		}
		var d dirTemplateData
		d.Glob = name
		d.Content = entries
		if err := dirTemplate.Execute(w, d); err != nil {
			log.Error.Printf("rendering directory template: %v", err)
		}
		return
	} else {
		httpError(w, err)
		return
	}

}

func (s *web) whichAccessFollowLinks(name upspin.PathName) (*upspin.DirEntry, error) {
	for loop := 0; loop < upspin.MaxLinkHops; loop++ {
		dir, err := s.cli.DirServer(name)
		if err != nil {
			return nil, err
		}
		entry, err := dir.WhichAccess(name)
		if err == upspin.ErrFollowLink {
			name = entry.Link
			continue
		}
		if err != nil {
			return nil, err
		}
		return entry, nil
	}
	return nil, nil
}

func httpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Match(errors.E(errors.Private), err),
		errors.Match(errors.E(errors.Permission), err):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case errors.Match(errors.E(errors.NotExist), err):
		http.Error(w, "Not Found", http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type dirTemplateData struct {
	// One and only one of these must be set.
	Dir  upspin.PathName
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
<h1>Index of {{.}}</h1>
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
