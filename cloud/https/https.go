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

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
)

// Options permits the configuration of TLS certificates for servers running
// outside GCE. The default is the self-signed certificate in
// upspin.io/grpc/auth/testdata.
type Options struct {
	// LetsEncryptCache specifies the cache file for Let's Encrypt.
	// If non-empty, enables Let's Encrypt certificates for this server.
	LetsEncryptCache string

	// CertFile and KeyFile specifies the TLS certificates to use.
	// It has no effect if LetsEncryptCache is non-empty.
	CertFile string
	KeyFile  string
}

var defaultOptions = &Options{
	CertFile: filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/grpc/auth/testdata/cert.pem"),
	KeyFile:  filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/grpc/auth/testdata/key.pem"),
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
// the address specified by addr using the certificate details specified by opt.
//
// The given channel, if any, is closed when the TCP listener has succeeded.
// It may be used to signal that the server is ready to start serving requests.
func ListenAndServe(ready chan<- struct{}, metaSuffix, addr string, opt *Options) {
	if opt == nil {
		opt = defaultOptions
	} else {
		opt.applyDefaults()
	}
	var config *tls.Config
	if metadata.OnGCE() {
		log.Info.Println("https: on GCE; serving HTTPS on port 443 using Let's Encrypt")
		var m letsencrypt.Manager
		const key = "letsencrypt-bucket"
		bucket, err := metadata.InstanceAttributeValue(key)
		if err != nil {
			log.Fatalf("https: couldn't read %q metadata value: %v", key, err)
		}
		if err := letsencryptCache(&m, bucket, metaSuffix); err != nil {
			log.Fatalf("https: couldn't set up letsencrypt cache: %v", err)
		}
		addr = ":443"
		config = &tls.Config{
			GetCertificate: m.GetCertificate,
		}
	} else if file := opt.LetsEncryptCache; file != "" {
		log.Info.Printf("https: serving HTTPS on %q using Let's Encrypt certificates", addr)
		var m letsencrypt.Manager
		if err := m.CacheFile(file); err != nil {
			log.Fatalf("https: couldn't set up letsencrypt cache: %v", err)
		}
		config = &tls.Config{
			GetCertificate: m.GetCertificate,
		}
	} else {
		log.Info.Printf("https: not on GCE; serving HTTPS on %q using provided certificates", addr)
		if opt.CertFile == defaultOptions.CertFile || opt.KeyFile == defaultOptions.KeyFile {
			log.Error.Print("https: WARNING: using self-signed test certificates.")
		}
		var err error
		config, err = newDefaultTLSConfig(opt.CertFile, opt.KeyFile)
		if err != nil {
			log.Fatalf("https: setting up TLS config: %v", err)
		}
	}
	server := &http.Server{
		TLSConfig: config,
	}
	// TODO(adg): enable HTTP/2 once it's fast enough
	//err := http2.ConfigureServer(server, nil)
	//if err != nil {
	//	log.Fatalf("https: %v", err)
	//}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("https: %v", err)
	}
	if ready != nil {
		close(ready)
	}
	err = server.Serve(tls.NewListener(ln, config))
	log.Fatalf("https: %v", err)
}

// ListenAndServeFromFlags is the same as ListenAndServe, but it determines the
// listen address and Options from command-line flags in the flags package.
func ListenAndServeFromFlags(ready chan<- struct{}, metaSuffix string) {
	ListenAndServe(ready, metaSuffix, flags.HTTPSAddr, &Options{
		LetsEncryptCache: flags.LetsEncryptCache,
		CertFile:         flags.TLSCertFile,
		KeyFile:          flags.TLSKeyFile,
	})
}

func letsencryptCache(m *letsencrypt.Manager, bucket, suffix string) error {
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
		// Watch the letsencrypt manager for changes and cache them.
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

// newDefaultTLSConfig creates a new TLS config based on the certificate files given.
func newDefaultTLSConfig(certFile string, certKeyFile string) (*tls.Config, error) {
	const op = "cloud/https.newDefaultTLSConfig"
	certReadable, err := isReadableFile(certFile)
	if err != nil {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("SSL certificate in %q: %q", certFile, err))
	}
	if !certReadable {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("certificate file %q not readable", certFile))
	}
	keyReadable, err := isReadableFile(certKeyFile)
	if err != nil {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("SSL key in %q: %v", certKeyFile, err))
	}
	if !keyReadable {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("certificate key file %q not readable", certKeyFile))
	}

	cert, err := tls.LoadX509KeyPair(certFile, certKeyFile)
	if err != nil {
		return nil, errors.E(op, err)
	}

	tlsConfig := &tls.Config{
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true, // Use our choice, not the client's choice
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		Certificates:             []tls.Certificate{cert},
	}
	tlsConfig.BuildNameToCertificate()
	return tlsConfig, nil
}

// isReadableFile reports whether the file exists and is readable.
// If the error is non-nil, it means there might be a file or directory
// with that name but we cannot read it.
func isReadableFile(path string) (bool, error) {
	// Is it stattable and is it a plain file?
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Item does not exist.
		}
		return false, err // Item is problematic.
	}
	if info.IsDir() {
		return false, errors.Str("is directory")
	}
	// Is it readable?
	fd, err := os.Open(path)
	if err != nil {
		return false, access.ErrPermissionDenied
	}
	fd.Close()
	return true, nil // Item exists and is readable.
}
