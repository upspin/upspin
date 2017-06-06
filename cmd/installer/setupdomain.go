// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"net/http"
	"strconv"

	"upspin.io/upspin"
)

// setupDomain allows the user to create or update their server names and when
// creating, helps the user set up the DNS entry and then invokes setupstorage
// and setupserver.
func (s *server) setupDomain(w http.ResponseWriter, r *http.Request) {
	index := r.FormValue("userindex")
	if index == "" {
		s.errorLine(w, "Internal: No userindex given as parameter")
		return
	}
	selected, err := strconv.Atoi(index)
	if err != nil {
		s.errorLinef(w, "Internal: converting userindex %s: %s", index, err)
		return
	}
	cfg := s.configs[selected]

	if s.configs[selected].DirEndpoint().Transport == upspin.Unassigned {
		// Looks like your directory server is not set up yet.
		// Please enter a new or existing server name.
		// ...
	}

	// TODO: To be continued...
	s.info(w, "Username:"+string(cfg.UserName())+" index: "+index, "Quit", "/exit")
}

var domainPickerTpl = template.Must(template.New("domainpicker").Parse(`
<div class="row">
<div class="col-md-2"></div>
<div class="col-md-8">
Do you want to create a new domain or use an existing one?
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
`))
