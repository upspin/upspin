// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg,andybons): make the HTML pages prettier

// Package web provides an http.Handler implementation that serves content from
// the Upspin namespace. For example, an HTTP request for
//   http://host.example.com/user@example.com/foo
// returns the Upspin file
//   user@example.com/foo
package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"upspin.io/access"
	"upspin.io/client"
	"upspin.io/client/clientutil"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// IsWriter is a method provided by serverutil/perm.Perm, but in the context of
// this package the method determines whether a user's Upspin tree should be
// served through this web interface.
type IsWriter interface {
	IsWriter(upspin.UserName) bool
}

// New returns an http.Handler that serves the Upspin names identified
// by the request path. For example, a request for a URL with the path
// "/user@example.com/file" returns the content available at that Upspin
// path (without the leading slash).
//
// The handler will only serve the Upspin trees of users that are considered
// Writers by the given IsWriter.
func New(cfg upspin.Config, perm IsWriter) http.Handler {
	return &web{
		cfg:  cfg,
		cli:  client.New(cfg),
		perm: perm,
	}
}

type web struct {
	cfg  upspin.Config
	cli  upspin.Client
	perm IsWriter
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
	case errors.Is(errors.NotExist, err),
		errors.Is(errors.BrokenLink, err):
		// Handle NotExist or BrokenLink later, as response
		// depends on whether 'All' has 'list' right.
	case err != nil:
		httpError(w, err)
		return
	default:
		// Update name as we may have followed a link.
		name = entry.Name
	}

	// Fetch read and list rights for AllUsers.
	readable, listable, err := s.accessAll(name)
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
		f, err := s.cli.Open(name)
		if err != nil {
			httpError(w, err)
			return
		}
		defer f.Close()
		http.ServeContent(w, r, string(name), time.Unix(int64(entry.Time), 0), f)
	default:
		code := http.StatusForbidden
		http.Error(w, http.StatusText(code), code)
	}
}

// whichAccess returns Access entry for path name, handling links.
func (s *web) whichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	dir, err := s.cli.DirServer(name)
	if err != nil {
		return nil, err
	}
	for {
		whichAccess, err := dir.WhichAccess(name)
		if err == upspin.ErrFollowLink {
			// If we get link error, go back up the path looking for
			// the Access file that rules over the link.
			name = path.DropPath(name, 1)
			continue
		}
		if err != nil {
			return nil, err
		}
		return whichAccess, nil
	}
}

// accessAll returns read and list rights of path for AllUsers.
func (s *web) accessAll(name upspin.PathName) (bool, bool, error) {
	// Get access entry.
	whichAccess, err := s.whichAccess(name)
	if err != nil {
		return false, false, err
	}
	// No Access file, no read nor list rights for AllUsers.
	if whichAccess == nil {
		return false, false, nil
	}
	accessData, err := clientutil.ReadAll(s.cfg, whichAccess)
	if err != nil {
		return false, false, err
	}
	acc, err := access.Parse(whichAccess.Name, accessData)
	if err != nil {
		return false, false, err
	}
	// Check read and list rights for AllUsers.
	readable, err := acc.Can(access.AllUsers, access.Read, name, s.cli.Get)
	if err != nil {
		return false, false, err
	}
	listable, err := acc.Can(access.AllUsers, access.List, name, s.cli.Get)
	if err != nil {
		return false, false, err
	}

	return readable, listable, nil
}

// ifError checks if the error is the expected one, and if so writes back an
// HTTP error of the corresponding code.
func ifError(w http.ResponseWriter, got error, want errors.Kind, code int) bool {
	if !errors.Is(want, got) {
		return false
	}
	http.Error(w, http.StatusText(code), code)
	return true
}

func httpError(w http.ResponseWriter, err error) {
	// This construction sets the HTTP error to the first type that matches.
	switch {
	case ifError(w, err, errors.Private, http.StatusForbidden):
	case ifError(w, err, errors.Permission, http.StatusForbidden):
	case ifError(w, err, errors.NotExist, http.StatusNotFound):
	case ifError(w, err, errors.BrokenLink, http.StatusNotFound):
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type dirTemplateData struct {
	Dir     upspin.PathName
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
