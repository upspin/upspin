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

func (s *server) errorLinef(w http.ResponseWriter, format string, a ...interface{}) {
	display(w, fmt.Sprintf(format, a...), "", "Exit", "/exit")
}

func (s *server) errorf(w http.ResponseWriter, header, format string, a ...interface{}) {
	display(w, header, fmt.Sprintf(format, a...), "Exit", "/exit")
}

// errorLine displays an error as a single line.
func (s *server) errorLine(w http.ResponseWriter, line string) {
	display(w, line, "", "Exit", "/exit")
}

// errorStr writes an error header with "An error occurred" followed by the
// detailed error string, which can be multi-line.
func (s *server) errorStr(w http.ResponseWriter, str string) {
	display(w, "An error occurred", str, "Exit", "/exit")
}

func (s *server) error(w http.ResponseWriter, err error) {
	s.errorStr(w, err.Error())
}

func (s *server) errorWithHeader(w http.ResponseWriter, header string, err error) {
	display(w, header, err.Error(), "Exit", "/exit")
}

// info writes a multi-line message followed by a navigation button with nextTxt
// as display and nextURL as the target URL.
func (s *server) info(w http.ResponseWriter, message, nextTxt, nextURL string) {
	display(w, "", message, nextTxt, nextURL)
}

// display displays the message preceeded by an optional error header and
// formats the navigation button with the nextTxt anchor and nextURL target.
func display(w http.ResponseWriter, errorHeader, message, nextTxt, nextURL string) {
	headerTpl.Execute(w, "")
	if len(errorHeader) > 0 {
		errorHeaderTpl.Execute(w, struct {
			ErrorHeader string
		}{
			ErrorHeader: errorHeader,
		})
	}
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

var errorHeaderTpl = template.Must(template.New("errorHeader").Parse(`
<div class="alert alert-danger" role="alert">
{{.ErrorHeader}}
</div>
`))
