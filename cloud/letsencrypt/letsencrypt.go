// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package letsencrypt provides a helper for starting an HTTPS server
// that uses letsencrypt, storing credentials in Google Cloud Storage.
package letsencrypt

import (
	"google.golang.org/cloud/compute/metadata"
	"rsc.io/letsencrypt"

	"upspin.io/cloud/letscloud"
	"upspin.io/log"
)

// ListenAndServe serves the http.DefaultServeMux by HTTPS (and HTTP,
// redirecting to HTTPS), storing SSL credentials in the Google Cloud Storage
// buckets nominated by the Google Compute Engine metadata variables
// "letscloud-get-url-metaSuffix" and "letscloud-put-url-metaSuffix", where
// metaSuffix is the supplied argument.
//
// See the upspin.io/cloud/letscloud package for more information.
func ListenAndServe(metaSuffix string) {
	var m letsencrypt.Manager
	v := func(key string) string {
		v, err := metadata.InstanceAttributeValue(key)
		if err != nil {
			log.Fatalf("letsencrypt: couldn't read %q metadata value: %v", key, err)
		}
		return v
	}
	get, put := v("letscloud-get-url-"+metaSuffix), v("letscloud-put-url-"+metaSuffix)
	if err := letscloud.Cache(&m, get, put); err != nil {
		log.Fatalf("letsencrypt: %v", err)
	}
	log.Fatalf("letsencrypt: %v", m.Serve())
}
