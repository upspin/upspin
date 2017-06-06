// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/dgryski/go-linebreak"
)

const maxWidth = 120

func (s *server) errorLinef(w http.ResponseWriter, format string, a ...interface{}) {
	display(w, fmt.Sprintf(format, a...), "", "Exit", "/exit", nil)
}

func (s *server) errorf(w http.ResponseWriter, header, format string, a ...interface{}) {
	display(w, header, fmt.Sprintf(format, a...), "Exit", "/exit", nil)
}

// errorLine displays an error as a single line.
func (s *server) errorLine(w http.ResponseWriter, line string) {
	display(w, line, "", "Exit", "/exit", nil)
}

// errorStr writes an error header with "An error occurred" followed by the
// detailed error string, which can be multi-line.
func (s *server) errorStr(w http.ResponseWriter, str string) {
	display(w, "An error occurred", str, "Exit", "/exit", nil)
}

func (s *server) error(w http.ResponseWriter, err error) {
	s.errorStr(w, err.Error())
}

func (s *server) errorWithHeader(w http.ResponseWriter, header string, err error) {
	display(w, header, err.Error(), "Exit", "/exit", nil)
}

// info writes a multi-line message followed by a navigation button with nextTxt
// as display and nextURL as the target URL.
func (s *server) info(w http.ResponseWriter, message, nextTxt, nextURL string) {
	display(w, "", message, nextTxt, nextURL, nil)
}

// display displays the message preceded by an optional error header and
// formats the navigation button with the nextTxt anchor and nextURL target. It
// also optionally sets the key-value pairs of parameters to pass to the next
// screen.
func display(w http.ResponseWriter, errorHeader, message, nextTxt, nextURL string, params map[string]string) {
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
		Params  map[string]string
	}{
		NextTxt: nextTxt,
		NextURL: nextURL,
		Params:  params,
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
