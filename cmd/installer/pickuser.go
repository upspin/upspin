// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"net/http"
	"path/filepath"

	"os"
	"os/exec"
	"upspin.io/config"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"
)

// pickUser shows the user a selection of available users in the user's home dir
// and the option to create a new one.
func (s *server) pickUser(w http.ResponseWriter, r *http.Request) {
	// Finds users available in $HOME/upspin/<username>/config
	matches, err := filepath.Glob(filepath.Join(s.homeDir, "upspin", "*", "config"))
	if err != nil {
		s.errorLinef(w, "path error: %s", err)
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

	userPickerTpl.Execute(w, struct {
		Cfg []upspin.Config
	}{
		Cfg: configs,
	})

	footerTpl.Execute(w, struct {
		NextTxt string
		NextURL string
		Params  []string
	}{
		NextTxt: "Next",
		NextURL: "/setupdomain",
	})

}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("username")

	user, suffix, domain, err := user.Parse(upspin.UserName(name))
	if err != nil {
		s.errorWithHeader(w, "Invalid username: "+name, err)
		return
	}
	if suffix != "" {
		s.errorLine(w, "Cannot create a suffixed user with this installer.")
		return
	}

	userName := user + "@" + domain // Canonicalized user name.

	// Create a new subdirectory in $HOMEDIR/upspin/<username>.
	dir := filepath.Join(s.homeDir, "upspin", userName)
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		s.errorWithHeader(w, "Can't create directory in "+s.homeDir, err)
		return
	}

	// Signup by invoking cmd/upspin signup with a placeholder for servers.
	// They will be corrected in the next screen.

	cmd := exec.Command("upspin", "signup", "-server", "example.com",
		"-where", dir, userName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.errorf(w, "Error running cmd/upspin signup: ", "%s: %s", err, out)
		return
	}
	s.info(w, string(out), "Next", "/setupdomain")
}

var userPickerTpl = template.Must(template.New("userPicker").Parse(`
<div class="row">
<div class="col-md-2"></div>
<div class="col-md-9">
Select a user or sign up with a new one:
</div>
<div class="col-md-2"></div>
</div>

<div class="row">
	<div class="col-md-2"></div>
	<div class="col-md-8">
      		<label class="radio-inline">
        		<b><input type="radio" name="user-picker-group" id="new-user" value="">New user</b>
      		</label>
      		<input type="text" id="new-user-input" placeholder="ann@example.com" size="30" value="" style="display:none">
  	</div>
	<div class="col-md-2"></div>
</div>

{{range $i, $cfg := .Cfg}}
<div class="row">
	<div class="col-md-2"></div>
	<div class="col-md-8">
      		<label class="radio-inline">
        		<input type="radio" name="user-picker-group" id="radio-{{$i}}" value="{{$cfg.UserName}}">{{$cfg.UserName}}
      		</label>
  	</div>
	<div class="col-md-2"></div>
</div>
{{end}}
`))
