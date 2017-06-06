// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package https provides a helper for starting an HTTPS server.
package https // import "upspin.io/cloud/https"

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/shutdown"
)

// Options permits the configuration of TLS certificates for servers running
// outside GCE. The default is the self-signed certificate in
// upspin.io/rpc/testdata.
type Options struct {
	// Addr specifies the host and port on which the server should listen.
	Addr string

	// AutocertCache provides a cache for use with Let's Encrypt.
	// If non-nil, enables Let's Encrypt certificates for this server.
	AutocertCache autocert.Cache

	// LetsEncryptCache specifies the cache file for Let's Encrypt.
	// If non-empty, enables Let's Encrypt certificates for this server.
	LetsEncryptCache string

	// LetsEncryptHosts specifies the list of hosts for which we should
	// obtain TLS certificates through Let's Encrypt. If LetsEncryptCache
	// is specified this should be specified also.
	LetsEncryptHosts []string

	// CertFile and KeyFile specifies the TLS certificates to use.
	// It has no effect if LetsEncryptCache is non-empty.
	CertFile string
	KeyFile  string

	// InsecureHTTP specifies whether to serve insecure HTTP without TLS.
	// An error occurs if this is attempted with a non-loopback address.
	InsecureHTTP bool
}

var defaultOptions = &Options{
	CertFile: filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/rpc/testdata/cert.pem"),
	KeyFile:  filepath.Join(os.Getenv("GOPATH"), "/src/upspin.io/rpc/testdata/key.pem"),
}

func (opt *Options) applyDefaults() {
	if opt.CertFile == "" {
		opt.CertFile = defaultOptions.CertFile
	}
	if opt.KeyFile == "" {
		opt.KeyFile = defaultOptions.KeyFile
	}
}

// OptionsFromFlags returns Options derived from the command-line flags present
// in the upspin.io/flags package.
func OptionsFromFlags() *Options {
	var hosts []string
	if host := string(flags.NetAddr); host != "" {
		// Make an effort to trim the :port suffix.
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		hosts = []string{host}
	}
	addr := flags.HTTPSAddr
	if flags.InsecureHTTP {
		addr = flags.HTTPAddr
	}
	return &Options{
		Addr:             addr,
		LetsEncryptCache: flags.LetsEncryptCache,
		LetsEncryptHosts: hosts,
		CertFile:         flags.TLSCertFile,
		KeyFile:          flags.TLSKeyFile,
		InsecureHTTP:     flags.InsecureHTTP,
	}
}

// ListenAndServeFromFlags is the same as ListenAndServe, but it determines the
// listen address and Options from command-line flags in the flags package.
func ListenAndServeFromFlags(ready chan<- struct{}) {
	ListenAndServe(ready, OptionsFromFlags())
}

// ListenAndServe serves the http.DefaultServeMux by HTTPS (and HTTP,
// redirecting to HTTPS) using the provided options.
//
// The given channel, if any, is closed when the TCP listener has succeeded.
// It may be used to signal that the server is ready to start serving requests.
//
// ListenAndServe does not return. It exits the program when the server is
// shut down (via SIGTERM or due to an error) and calls shutdown.Shutdown.
func ListenAndServe(ready chan<- struct{}, opt *Options) {
	if opt == nil {
		opt = defaultOptions
	} else {
		opt.applyDefaults()
	}

	var m autocert.Manager
	m.Prompt = autocert.AcceptTOS
	if h := opt.LetsEncryptHosts; len(h) > 0 {
		m.HostPolicy = autocert.HostWhitelist(h...)
	}

	addr := opt.Addr
	var config *tls.Config
	if opt.InsecureHTTP {
		log.Info.Printf("https: serving insecure HTTP on %q", addr)
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			log.Fatalf("https: couldn't parse address: %v", err)
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			log.Fatalf("https: cannot serve insecure HTTP on non-loopback address %q", addr)
		}
	} else if dir := opt.LetsEncryptCache; dir != "" {
		log.Info.Printf("https: serving HTTPS on %q using Let's Encrypt certificates", addr)
		if err := os.MkdirAll(dir, 0700); err != nil {
			log.Fatalf("https: could not create or read -letscache directory: %v", err)
		}
		m.Cache = autocert.DirCache(dir)
		config = &tls.Config{GetCertificate: m.GetCertificate}
	} else if cache := opt.AutocertCache; cache != nil {
		addr = ":443"
		log.Info.Printf("https: serving HTTPS on %q using Let's Encrypt certificates", addr)
		m.Cache = cache
		config = &tls.Config{GetCertificate: m.GetCertificate}
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
	// WriteTimeout is set to 0 because it also pertains to streaming
	// replies, e.g., the DirServer.Watch interface.
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
		TLSConfig:         config,
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
	shutdown.Handle(func() {
		// Stop accepting connections and forces the server to stop
		// its serving loop.
		ln.Close()
	})
	if !opt.InsecureHTTP {
		ln = tls.NewListener(ln, config)
	}
	err = server.Serve(ln)
	log.Printf("https: %v", err)
	shutdown.Now(1)
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
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true, // Use our choice, not the client's choice
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256, tls.X25519},
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
