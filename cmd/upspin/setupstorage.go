// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func (s *State) setupstorage(args ...string) {
	const (
		help = `
setupstorage
`
	)
	fs := flag.NewFlagSet("setupstorage", flag.ExitOnError)
	domain := fs.String("domain", "", "`domain` name")
	s.parseFlags(fs, args, help, "[-project=<gcp_project_name>] setupstorage -domain=<name> my-bucket")
	_ = domain
}
