// Package serverauth provides authentication and SSL functionality common to all Upspin servers.
package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	userServiceAddr = "https://upspin.io:8082"
)

// NewSecureServer returns an HTTP server setup with the certificate and key as provided by local file names, bound to the requested port.
func NewSecureServer(port int, certFile string, certKeyFile string) (*http.Server, error) {
	certReadable, err := isReadableFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("Problem with SSL certificate in %q: %q", certFile, err)
	}
	if !certReadable {
		return nil, fmt.Errorf("Certificate %q not readable", certFile)
	}
	keyReadable, err := isReadableFile(certKeyFile)
	if err != nil {
		return nil, fmt.Errorf("Problem with SSL key %q: %v", certKeyFile, err)
	}
	if !keyReadable {
		return nil, fmt.Errorf("Certificate key %q not readable", certKeyFile)
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
	}
	tlsConfig.BuildNameToCertificate()

	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", port),
		TLSConfig: tlsConfig,
	}
	return server, nil
}

// PublicUserLookupService returns a Lookup function that looks up users keys and endpoints.
// The lookup function returned is bound to a well-known public Upspin user service.
func PublicUserLookupService() func(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	context := &upspin.Context{}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(userServiceAddr),
	}
	u, err := bind.User(context, e)
	if err != nil {
		log.Fatalf("Can't bind to User service: %v", err)
	}
	return u.Lookup
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
		return false, errors.New("is directory")
	}
	// Is it readable?
	fd, err := os.Open(path)
	if err != nil {
		return false, errors.New("permission denied")
	}
	fd.Close()
	return true, nil // Item exists and is readable.
}
