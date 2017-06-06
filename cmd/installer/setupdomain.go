// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"html/template"
	"net/http"
)

// setupDomain allows the user to create or update their server names and when
// creating, helps the user set up the DNS entry and then invokes setupstorage
// and setupserver.
func (s *server) setupDomain(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("username")
	if name == "" {
		s.errorLine(w, "Internal: No username given as parameter")
		return
	}

	// Do you want to set up a new domain for $username or use one of the
	// domains below?

	// TODO: To be continued...
	s.info(w, "Username:"+name, "Quit", "/exit")
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
