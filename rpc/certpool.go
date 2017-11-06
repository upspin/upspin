// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"crypto/x509"
	"io/ioutil"
	"path/filepath"
	"sync"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var certPoolCache struct {
	sync.Mutex
	m map[string]*x509.CertPool // [dir]pool
}

// CertPoolFromConfig returns the TLS certificate pool for this config.
func CertPoolFromConfig(cfg upspin.Config) (*x509.CertPool, error) {
	dir := cfg.Value("tlscerts")
	if dir == "" {
		return nil, nil
	}
	certPoolCache.Lock()
	defer certPoolCache.Unlock()
	if p := certPoolCache.m[dir]; p != nil {
		return p, nil
	}
	p, err := certPoolFromDir(dir)
	if err == nil {
		if certPoolCache.m == nil {
			certPoolCache.m = make(map[string]*x509.CertPool)
		}
		certPoolCache.m[dir] = p
	}
	return p, err
}

// certPoolFromDir parses any PEM files in the provided directory
// and returns the resulting pool.
func certPoolFromDir(dir string) (*x509.CertPool, error) {
	var pool *x509.CertPool
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, errors.Errorf("reading TLS Certificates in %q: %v", dir, err)
	}
	for _, fi := range fis {
		name := fi.Name()
		if filepath.Ext(name) != ".pem" {
			continue
		}
		pem, err := ioutil.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, errors.Errorf("reading TLS Certificate %q: %v", name, err)
		}
		if pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pem)
	}
	return pool, nil
}
