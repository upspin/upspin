// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package https provides a helper for starting an HTTPS server
// that uses letsencrypt, storing credentials in Google Cloud Storage.
package https

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"google.golang.org/cloud/compute/metadata"
	"rsc.io/letsencrypt"

	"upspin.io/auth"
	"upspin.io/cloud/letscloud"
	"upspin.io/log"
)

// Options permits the configuration of TLS certificates for servers running
// outside GCE. The default is the self-signed certificate in
// upspin.io/auth/grpcauth/testdata.
type Options struct {
	CertFile    string
	CertKeyFile string
}

var defaultOptions = &Options{
	CertFile:    filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/cert.pem"),
	CertKeyFile: filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/key.pem"),
}

func (opt *Options) applyDefaults() {
	if opt.CertFile == "" {
		opt.CertFile = defaultOptions.CertFile
	}
	if opt.CertKeyFile == "" {
		opt.CertKeyFile = defaultOptions.CertKeyFile
	}
}

// ListenAndServe serves the http.DefaultServeMux by HTTPS (and HTTP,
// redirecting to HTTPS), storing SSL credentials in the Google Cloud Storage
// buckets nominated by the Google Compute Engine project metadata variables
// "letscloud-get-url-metaSuffix" and "letscloud-put-url-metaSuffix", where
// metaSuffix is the supplied argument.
// (See the upspin.io/cloud/letscloud package for more information.)
//
// If the server is running outside GCE, instead an HTTPS server is started on
// the address specified by addr the certificate and key files specified by
// opt.
func ListenAndServe(metaSuffix, addr string, opt *Options) {
	if opt == nil {
		opt = defaultOptions
	} else {
		opt.applyDefaults()
	}
	if metadata.OnGCE() {
		log.Println("https: on GCE; serving HTTPS on port 443 using Let's Encrypt")
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

	log.Printf("https: not on GCE; serving HTTPS on %q", addr)
	if opt.CertFile == defaultOptions.CertFile || opt.CertKeyFile == defaultOptions.CertKeyFile {
		log.Print("https: WARNING: using self-signed test certificates.")
	}
	config, err := auth.NewDefaultTLSConfig(opt.CertFile, opt.CertKeyFile)
	if err != nil {
		log.Fatalf("https: setting up TLS config: %v", err)
	}
	config.NextProtos = []string{"h2"} // Enable HTTP/2 support
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("https: %v", err)
	}
	err = http.Serve(tls.NewListener(ln, config), nil)
	log.Fatalf("https: %v", err)
}
