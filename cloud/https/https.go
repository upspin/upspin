// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package https provides a helper for starting an HTTPS server.
package https // import "upspin.io/cloud/https"

import (
	"crypto/tls"
	"go/build"
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
	"upspin.io/serverutil"
	"upspin.io/shutdown"
)

// Options permits the configuration of TLS certificates for servers running
// outside GCE. The default is the self-signed certificate in
// upspin.io/rpc/testdata.
type Options struct {
	// Addr specifies the host and port on which the server should serve
	// HTTPS requests (or HTTP requests if InsecureHTTP is set).
	// If empty, ":443" is used.
	Addr string

	// HTTPAddr specifies the host and port on which the server should
	// serve HTTP requests. If empty and InsecureHTTP is true, Addr is
	// used.  If empty otherwise, ":80" is used.
	HTTPAddr string

	// AutocertCache provides a cache for use with Let's Encrypt.
	// If non-nil, enables Let's Encrypt certificates for this server.
	// See the comment on ErrAutocertCacheMiss before usin this feature.
	AutocertCache AutocertCache

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

// AutocertCache is a copy of the autocert.Cache interface, provided here so
// that implementers need not import the autocert package directly.
// See ErrAutocertCacheMiss for more details.
type AutocertCache interface {
	autocert.Cache
}

// ErrAutocertCacheMiss is a copy of the autocert.ErrCacheMiss variable that
// must be used by any AutocertCache implementations used in the Options
// struct. This is because the autocert package is vendored by the upspin.io
// repository, and so an outside implementation that returns ErrCacheMiss from
// another version of the package will return an error value that is not
// recognized by the autocert package.
var ErrAutocertCacheMiss = autocert.ErrCacheMiss

var defaultOptions = &Options{
	CertFile: filepath.Join(testKeyDir, "cert.pem"),
	KeyFile:  filepath.Join(testKeyDir, "key.pem"),
}

var testKeyDir = findTestKeyDir() // Do this just once.

// findTestKeyDir locates the "rpc/testdata" directory within the upspin.io
// repository in a Go workspace and returns its absolute path.
// If the upspin.io repository cannot be found, it returns ".".
func findTestKeyDir() string {
	p, err := build.Import("upspin.io/rpc/testdata", "", build.FindOnly)
	if err != nil {
		return "."
	}
	return p.Dir
}

func (opt *Options) applyDefaults() {
	if opt.Addr == "" {
		opt.Addr = ":443"
	}
	if opt.HTTPAddr == "" {
		if opt.InsecureHTTP {
			opt.HTTPAddr = opt.Addr
		} else {
			opt.HTTPAddr = ":80"
		}
	}
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
		HTTPAddr:         flags.HTTPAddr,
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

	hasLetsEncryptCache := opt.LetsEncryptCache != ""
	hasAutocertCache := opt.AutocertCache != nil
	hasCert := opt.CertFile != defaultOptions.CertFile || opt.KeyFile != defaultOptions.KeyFile

	var manager autocert.Manager
	manager.Prompt = autocert.AcceptTOS
	if h := opt.LetsEncryptHosts; len(h) > 0 {
		manager.HostPolicy = autocert.HostWhitelist(h...)
	}

	addr := opt.Addr
	var config *tls.Config
	switch {
	case opt.InsecureHTTP:
		log.Info.Printf("https: serving insecure HTTP on %q", addr)
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			log.Fatalf("https: couldn't parse address: %v", err)
		}
		if !serverutil.IsLoopback(host) {
			log.Error.Printf("https: WARNING: serving insecure HTTP on non-loopback address %q", addr)
		}
	case hasLetsEncryptCache && !hasAutocertCache && !hasCert:
		// The -letscache has a default value, so only take this path
		// if the other options are not selected.
		dir := opt.LetsEncryptCache
		log.Info.Printf("https: serving HTTPS on %q using Let's Encrypt certificates", addr)
		log.Info.Printf("https: caching Let's Encrypt certificates in %v", dir)
		if err := os.MkdirAll(dir, 0700); err != nil {
			log.Fatalf("https: could not create -letscache directory: %v", err)
		}
		manager.Cache = autocert.DirCache(dir)
		config = &tls.Config{GetCertificate: manager.GetCertificate}
	case hasAutocertCache:
		log.Info.Printf("https: serving HTTPS on %q using Let's Encrypt certificates", addr)
		manager.Cache = opt.AutocertCache
		config = &tls.Config{GetCertificate: manager.GetCertificate}
	default:
		log.Info.Printf("https: serving HTTPS on %q using provided certificates", addr)
		if opt.CertFile == defaultOptions.CertFile || opt.KeyFile == defaultOptions.KeyFile {
			log.Error.Print("https: WARNING: using self-signed test certificates.")
		}
		var err error
		config, err = newDefaultTLSConfig(opt.CertFile, opt.KeyFile)
		if err != nil {
			log.Fatalf("https: setting up TLS config: %v", err)
		}
	}

	// Set up the main listener for HTTPS (or HTTP if InsecureHTTP is set).
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("https: %v", err)
	}
	shutdown.Handle(func() { ln.Close() })

	httpLogger := log.NewStdLogger(log.Info)
	if manager.Cache != nil {
		// If we're using LetsEncrypt then we need to serve the http-01
		// challenge by plain HTTP. We also serve a redirect to HTTPS
		// for all other requests.
		httpLn, err := net.Listen("tcp", opt.HTTPAddr)
		if err != nil {
			log.Fatalf("https: %v", err)
		}
		shutdown.Handle(func() { httpLn.Close() })
		httpServer := &http.Server{
			Handler:  manager.HTTPHandler(nil),
			ErrorLog: httpLogger,
		}
		go func() {
			err := httpServer.Serve(httpLn)
			log.Printf("https: %v", err)
			shutdown.Now(1)
		}()
	}

	if ready != nil {
		// Notify the calling packages that
		// we're ready to accept requests.
		close(ready)
	}

	// If we're serving HTTPS then wrap the listener with a TLS listener.
	if !opt.InsecureHTTP {
		ln = tls.NewListener(ln, config)
	}

	// Set up the main server.
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout is set to 0 because it also pertains to
		// streaming replies, e.g., the DirServer.Watch interface.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
		TLSConfig:    config,
		ErrorLog:     httpLogger,
	}
	// TODO(adg): enable HTTP/2 once it's fast enough
	//err := http2.ConfigureServer(server, nil)
	//if err != nil {
	//	log.Fatalf("https: %v", err)
	//}
	err = server.Serve(ln)
	log.Printf("https: %v", err)
	shutdown.Now(1)
}

// newDefaultTLSConfig creates a new TLS config based on the certificate files given.
func newDefaultTLSConfig(certFile string, certKeyFile string) (*tls.Config, error) {
	const op errors.Op = "cloud/https.newDefaultTLSConfig"
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
