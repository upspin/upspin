// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/dgryski/go-linebreak"
	"html/template"
)

const maxWidth = 120

func (s *server) errorf(w http.ResponseWriter, format string, a ...interface{}) {
	s.errorStr(w, fmt.Sprintf(format, a))
}

func (s *server) errorStr(w http.ResponseWriter, str string) {
	s.info(w, str, "Exit", "/exit")
}

func (s *server) error(w http.ResponseWriter, err error) {
	s.errorStr(w, err.Error())
}

func (s *server) info(w http.ResponseWriter, message, nextTxt, nextURL string) {
	headerTpl.Execute(w, "")
	infoTpl.Execute(w, struct {
		Text []string
	}{
		Text: strings.Split(linebreak.Wrap(message, maxWidth, maxWidth), "\n"),
	})
	footerTpl.Execute(w, struct {
		NextTxt string
		NextURL string
		Params  []string
	}{
		NextTxt: nextTxt,
		NextURL: nextURL,
	})
}

var infoTpl = template.Must(template.New("info").Parse(`
{{range .Text}}
<div class="row">
<div class="col-md-12">{{.}}</div>
</div>
{{end}}
`))
