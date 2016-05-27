// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package https provides a helper for starting an HTTPS server
// that uses letsencrypt, storing credentials in Google Cloud Storage.
//
// It registers the -http_addr flag with the flag package,
// for use when the server is run outside of GCE.
package https

import (
	"flag"
	"net/http"

	"google.golang.org/cloud/compute/metadata"
	"rsc.io/letsencrypt"

	"upspin.io/cloud/letscloud"
	"upspin.io/log"
)

var (
	httpAddr   = flag.String("http_addr", "localhost:8080", "HTTP listen address (not used in production)")
	selfSigned = flag.Bool("self_signed", false, "Use self-signed TLS certificates")
)

// ListenAndServe serves the http.DefaultServeMux by HTTPS (and HTTP,
// redirecting to HTTPS), storing SSL credentials in the Google Cloud Storage
// buckets nominated by the Google Compute Engine project metadata variables
// "letscloud-get-url-metaSuffix" and "letscloud-put-url-metaSuffix", where
// metaSuffix is the supplied argument.
//
// See the upspin.io/cloud/letscloud package for more information.
//
// If the server is running outside GCE, instead an insecure HTTP server is
// started on the address specified by the -http_addr flag.
func ListenAndServe(metaSuffix string) {
	if !metadata.OnGCE() {
		// TODO(adg): use selfSigned
		log.Printf("https: not on GCE; serving insecure HTTP on %q", *httpAddr)
		err := http.ListenAndServe(*httpAddr, nil)
		log.Fatalf("https: %v", err)
	}

	var m letsencrypt.Manager
	v := func(key string) string {
		v, err := metadata.ProjectAttributeValue(key)
		if err != nil {
			log.Fatalf("https: couldn't read %q metadata value: %v", key, err)
		}
		return v
	}
	get, put := v("letscloud-get-url-"+metaSuffix), v("letscloud-put-url-"+metaSuffix)
	if err := letscloud.Cache(&m, get, put); err != nil {
		log.Fatalf("https: %v", err)
	}
	log.Fatalf("https: %v", m.Serve())
}
