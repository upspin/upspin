// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package https provides a helper for starting an HTTPS server.
package https

import (
	"context"
	"crypto/tls"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"google.golang.org/api/option"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"rsc.io/letsencrypt"

	"upspin.io/auth"
	"upspin.io/log"
)

// Options permits the configuration of TLS certificates for servers running
// outside GCE. The default is the self-signed certificate in
// upspin.io/auth/grpcauth/testdata.
type Options struct {
	CertFile string
	KeyFile  string
}

var defaultOptions = &Options{
	CertFile: filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/cert.pem"),
	KeyFile:  filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/auth/grpcauth/testdata/key.pem"),
}

func (opt *Options) applyDefaults() {
	if opt.CertFile == "" {
		opt.CertFile = defaultOptions.CertFile
	}
	if opt.KeyFile == "" {
		opt.KeyFile = defaultOptions.KeyFile
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
		log.Info.Println("https: on GCE; serving HTTPS on port 443 using Let's Encrypt")
		var m letsencrypt.Manager
		const key = "letsencrypt-bucket"
		bucket, err := metadata.InstanceAttributeValue(key)
		if err != nil {
			log.Fatalf("https: couldn't read %q metadata value: %v", key, err)
		}
		if err := cloudCache(&m, bucket, metaSuffix); err != nil {
			log.Fatalf("https: couldn't set up letsencrypt cache: %v", err)
		}
		log.Fatalf("https: %v", m.Serve())
	}

	log.Info.Printf("https: not on GCE; serving HTTPS on %q", addr)
	if opt.CertFile == defaultOptions.CertFile || opt.KeyFile == defaultOptions.KeyFile {
		log.Error.Print("https: WARNING: using self-signed test certificates.")
	}
	config, err := auth.NewDefaultTLSConfig(opt.CertFile, opt.KeyFile)
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

func cloudCache(m *letsencrypt.Manager, bucket, suffix string) error {
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithScopes(storage.ScopeFullControl))
	if err != nil {
		return err
	}
	obj := client.Bucket(bucket).Object("letsencrypt-" + suffix)

	// Try to read the existing cache value, if present.
	r, err := obj.NewReader(ctx)
	if err != storage.ErrObjectNotExist {
		if err != nil {
			return err
		}
		data, err := ioutil.ReadAll(r)
		r.Close()
		if err != nil {
			return err
		}
		if err := m.Unmarshal(string(data)); err != nil {
			return err
		}
	}

	go func() {
		for range m.Watch() {
			w := obj.NewWriter(ctx)
			_, err := io.WriteString(w, m.Marshal())
			if err != nil {
				log.Printf("https: writing letsencrypt cache: %v", err)
				continue
			}
			if err := w.Close(); err != nil {
				log.Printf("https: writing letsencrypt cache: %v", err)
			}
		}
	}()

	return nil
}
