// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "net/http"

// setupDomain allows the user to create or update their server names and when
// creating, helps the user set up the DNS entry and then invokes setupstorage
// and setupserver.
func (s *server) setupDomain(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("username")
	if name == "" {
		s.errorLine(w, "Internal: No username given as parameter")
		return
	}

	// TODO: To be continued...
	s.info(w, "Username:"+name, "Quit", "/exit")
}
