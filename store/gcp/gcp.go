// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements upspin.Store using Google Cloud Platform as its storage.
package gcp

import (
	"bytes"
	"fmt"
	"net/url"
	"sync"

	"upspin.io/bind"
	"upspin.io/cloud/storage"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/upspin"

	// We use GCS as the backing for our data.
	_ "upspin.io/cloud/storage/gcs"
)

// Server implements upspin.Store.
type server struct {
	context  upspin.Context
	endpoint upspin.Endpoint

	mu      sync.Mutex
	backend storage.Storage
}

var _ upspin.Store = (*server)(nil)

// New returns a new, unconfigured Store bound to the user in the context.
func New(context upspin.Context) upspin.Store {
	return &server{
		context: context.Copy(), // Make a copy to prevent user making further changes.
	}
}

var errNotConfigured = errors.E(errors.Invalid, errors.Str("GCP Store service not configured"))

func (s *server) storage() (storage.Storage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend == nil {
		return nil, errNotConfigured
	}
	return s.backend, nil
}

// Put implements upspin.Store.
func (s *server) Put(data []byte) (upspin.Reference, error) {
	const Put = "Put"
	// TODO: check that userName has permission to write to this store server.
	storage, err := s.storage()
	if err != nil {
		return "", errors.E(Put, err)
	}
	ref := sha256key.NewShaReader(bytes.NewReader(data)).EncodedSum()
	if _, err := storage.Put(ref, data); err != nil {
		return "", errors.E(Put, err)
	}
	return upspin.Reference(ref), nil
}

// Get implements upspin.Store.
func (s *server) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	const Get = "Get"
	storage, err := s.storage()
	if err != nil {
		return nil, nil, errors.E(Get, err)
	}
	link, err := storage.Get(string(ref))
	if err != nil {
		return nil, nil, errors.E(Get, err)
	}
	// GCP should return an valid HTTPS URL.
	u, err := url.Parse(link)
	if err != nil {
		err = errors.E(Get, errors.Errorf("can't parse url: %v: %v", link, err))
		return nil, nil, err
	}
	if u.Scheme != "https" {
		err = errors.E(Get, errors.Errorf("invalid scheme in GCP URL: %v", u))
		return nil, nil, err
	}
	loc := upspin.Location{
		Reference: upspin.Reference(link),
		// NetAddr is important so we can both ping the server and also cache the
		// HTTPS transport client efficiently.
		Endpoint: upspin.Endpoint{
			Transport: upspin.HTTPS,
			NetAddr:   upspin.NetAddr(fmt.Sprintf("%s://%s", u.Scheme, u.Host)),
		},
	}
	log.Debug.Printf("Ref %s returned as link: %s", ref, link)
	return nil, []upspin.Location{loc}, nil
}

// Delete implements upspin.Store.
func (s *server) Delete(ref upspin.Reference) error {
	const Delete = "Delete"
	storage, err := s.storage()
	if err != nil {
		return errors.E(Delete, err)
	}
	// TODO: verify ownership and proper ACLs to delete blob
	if err := storage.Delete(string(ref)); err != nil {
		return errors.E(Delete, errors.Errorf("%s: %s", ref, err))
	}
	return nil
}

// Dial implements upspin.Service.
func (s *server) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.GCP {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}
	return &server{
		context:  context.Copy(),
		endpoint: e,
	}, nil
}

// Configure configures the connection to the backing store (namely, GCP) once the service
// has been dialed. The details of the configuration are explained at the package comments.
func (s *server) Configure(options ...string) error {
	const Configure = "Configure"

	var dialOpts []storage.DialOpts
	for _, opt := range options {
		dialOpts = append(dialOpts, storage.WithOptions(opt))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend != nil {
		return errors.E(Configure, errors.Errorf("attempt to reconfigure configured GCP storage"))
	}
	client, err := storage.Dial("GCS", dialOpts...)
	if err != nil {
		return errors.E(Configure, err)
	}
	s.backend = client

	log.Debug.Printf("Configured GCP store: %v", options)
	return nil
}

// Ping implements upspin.Service.
func (s *server) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (s *server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.backend == nil {
		log.Error.Printf("Closing non-dialed gcp store")
		return
	}
	s.backend.Close()
}

// Authenticate implements upspin.Service.
func (s *server) Authenticate(upspin.Context) error {
	// Authentication is not dealt here. It happens at other layers.
	return nil
}

// Endpoint implements upspin.Service.
func (s *server) Endpoint() upspin.Endpoint {
	return s.endpoint
}

func init() {
	bind.RegisterStore(upspin.GCP, New(context.New()))
}
