// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/dgryski/go-linebreak"
	"upspin.io/config"
	"upspin.io/log"
	"upspin.io/upspin"
)

// pickUser shows the user a selection of available users in the user's home dir
// and the option to create a new one.
func (s *server) pickUser(w http.ResponseWriter, r *http.Request) {
	// Finds users available in $HOME/upspin/<username>/config
	matches, err := filepath.Glob(filepath.Join(s.homeDir, "upspin", "*", "config"))
	if err != nil {
		s.errorf(w, "path error: %s", err)
		return
	}
	var configs []upspin.Config
	for _, c := range matches {
		cfg, err := config.FromFile(c)
		if err != nil {
			log.Printf("Error parsing config %s: %s", c, err)
			continue
		}
		configs = append(configs, cfg)
	}

	// Show menu with options of users to select from.

	headerTpl.Execute(w, struct {
		Text []string
	}{})

	footerTpl.Execute(w, struct {
		NextTxt string
		NextURL string
		Params  []string
	}{
		NextTxt: "Next",
		NextURL: "/loaduser",
	})

	s.info(w, strings.Join(matches, "\n"), "Next", "/select")
}

var userPickerTpl = template.Must(template.New("userPicker").Parse(`
<div class="col-lg-6">
    <div class="input-group">
      <span class="input-group-addon">
        <input type="radio" aria-label="Radio button for following text input">
      </span>
      <input type="text" class="form-control" aria-label="Text input with radio button">
    </div>
  </div>
</div>
`))
