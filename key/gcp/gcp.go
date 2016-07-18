// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements the user service upspin.KeyServer on the Google Cloud Platform (GCP).
package gcp

import (
	"encoding/json"
	"strings"
	"sync"

	"upspin.io/bind"
	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"

	// We use GCS as the backing for our data.
	_ "upspin.io/cloud/storage/gcs"
)

// key is the implementation of the KeyServer Service on GCP.
type key struct {
	context     upspin.Context
	endpoint    upspin.Endpoint
	cloudClient storage.Storage
}

var _ upspin.KeyServer = (*key)(nil)

// userEntry stores all known information for a given user. The fields
// are exported because JSON parsing needs access to them.
type userEntry struct {
	User      upspin.UserName    // User's email address (e.g. bob@bar.com).
	Keys      []upspin.PublicKey // Known keys for the user.
	Endpoints []upspin.Endpoint  // Known endpoints for the user's directory entry.
}

const (
	minKeyLen = 12
)

var (
	errKeyTooShort     = errors.E(errors.Invalid, errors.Str("key length too short"))
	errInvalidUserName = errors.E(errors.Invalid, errors.Str("invalid user name format"))
)

var (
	mu       sync.Mutex // protects fields below
	refCount uint64
)

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

func isKeyInSlice(key upspin.PublicKey, slice []upspin.PublicKey) bool {
	for _, k := range slice {
		if key == k {
			return true
		}
	}
	return false
}

// AddKey adds a new public key for a user.
// TODO: this is not used yet, but useful in the future and was supported by the HTTP RESTful user server, so keeping it
// around for re-using later.
func (u *key) AddKey(userName upspin.UserName, key upspin.PublicKey) error {
	const AddKey = "AddKey"
	// Validate user name
	_, err := path.Parse(upspin.PathName(userName) + "/")
	if err != nil {
		return errors.E(AddKey, userName, errInvalidUserName)
	}
	if len(key) < minKeyLen {
		return errors.E(AddKey, userName, errKeyTooShort)
	}

	// Appends to the current user entry, if any.
	ue, err := u.fetchUserEntry(AddKey, userName)
	if err != nil {
		// If this is a Not Found error, then allocate a new userEntry and continue.
		if isNotFound(err) {
			log.Printf("User %q not found on GCP, adding new one", userName)
			ue = &userEntry{
				User: upspin.UserName(userName),
				Keys: make([]upspin.PublicKey, 0, 1),
			}
		} else {
			return err
		}
	}
	// Check that the key is not already there.
	if !isKeyInSlice(key, ue.Keys) {
		// Place key at head of slice to indicate higher priority.
		ue.Keys = append([]upspin.PublicKey{key}, ue.Keys...)
		err = u.putUserEntry(AddKey, userName, ue)
		if err != nil {
			return err
		}
	}
	log.Printf("Added key %s for user %v\n", key, userName)
	return nil
}

// AddRoot adds a new root endpoint for a user.
// TODO: this is not used yet, but useful in the future and was supported by the HTTP RESTful user server, so keeping it
// around for re-using later.
func (u *key) AddRoot(userName upspin.UserName, endpoint upspin.Endpoint) error {
	const AddRoot = "AddRoot"
	// Validate user name
	_, err := path.Parse(upspin.PathName(userName) + "/")
	if err != nil {
		return errors.E(AddRoot, userName, errInvalidUserName)
	}

	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(AddRoot, userName)
	if err != nil {
		// If this is a Not Found error, then allocate a new userEntry and continue.
		if isNotFound(err) {
			log.Printf("User %q not found on GCP, adding new one", userName)
			ue = &userEntry{
				User:      upspin.UserName(userName),
				Endpoints: make([]upspin.Endpoint, 0, 1),
			}
		} else {
			return err
		}
	}
	// Place the endpoint at the head of the slice to indicate higher priority.
	ue.Endpoints = append([]upspin.Endpoint{endpoint}, ue.Endpoints...)
	err = u.putUserEntry(AddRoot, userName, ue)
	if err != nil {
		return err
	}
	log.Printf("Added root %v for user %v", endpoint, userName)
	return nil
}

// Lookup implements upspin.KeyServer.
func (u *key) Lookup(userName upspin.UserName) (*upspin.User, error) {
	const Lookup = "Lookup"
	// Validate user name
	_, err := path.Parse(upspin.PathName(userName) + "/")
	if err != nil {
		return nil, errors.E(Lookup, userName, errInvalidUserName)
	}
	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(Lookup, userName)
	if err != nil {
		return nil, err
	}
	user := &upspin.User{
		Name:      userName,
		Dirs:      ue.Endpoints,
		PublicKey: ue.Keys[0],
	}
	return user, nil
}

// Put implements upspin.KeyServer.
func (u *key) Put(user *upspin.User) error {
	return errors.E("Put", errors.Syntax, errors.Str("not implemented"))
}

// fetchUserEntry reads the user entry for a given user from permanent storage on GCP.
func (u *key) fetchUserEntry(op string, userName upspin.UserName) (*userEntry, error) {
	// Get the user entry from GCP
	log.Printf("Going to get user entry on GCP for user %s", userName)
	buf, err := u.cloudClient.Download(string(userName))
	if err != nil {
		log.Printf("Error downloading user entry for %q: %q", userName, err)
		return nil, errors.E(op, userName, errors.NotExist, err)
	}
	// Now convert it to a userEntry
	var ue userEntry
	err = json.Unmarshal(buf, &ue)
	if err != nil {
		return nil, errors.E(op, userName, errors.IO, err)
	}
	log.Printf("Fetched user entry for %s", userName)
	return &ue, nil
}

// putUserEntry writes the user entry for a user to permanent storage on GCP.
func (u *key) putUserEntry(op string, userName upspin.UserName, userEntry *userEntry) error {
	if userEntry == nil {
		return errors.E(op, errors.Invalid, userName, errors.Str("nil userEntry"))
	}
	jsonBuf, err := json.Marshal(userEntry)
	if err != nil {
		return errors.E(op, errors.Invalid, userName, errors.Errorf("conversion to JSON failed: %v", err))
	}
	_, err = u.cloudClient.Put(string(userName), jsonBuf)
	if err != nil {
		return errors.E(op, userName, err)
	}
	return nil
}

// Configure configures an instance of the user service.
// Required configuration options are listed at the package comments.
func (u *key) Configure(options ...string) error {
	const Configure = "Configure"

	var dialOpts []storage.DialOpts
	// All options are for the Storage layer.
	for _, option := range options {
		dialOpts = append(dialOpts, storage.WithOptions(option))
	}

	var err error
	u.cloudClient, err = storage.Dial("GCS", dialOpts...)
	if err != nil {
		return errors.E(Configure, err)
	}
	log.Debug.Printf("Configured GCP user: %v", options)
	return nil
}

// isServiceConfigured reports whether the user service has been configured via a Configure call.
func (u *key) isServiceConfigured() bool {
	return u.cloudClient != nil && u.context.UserName() != ""
}

// Dial implements upspin.Service.
func (u *key) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.GCP {
		return nil, errors.E("Dial", errors.Invalid, errors.Str("unrecognized transport"))
	}
	mu.Lock()
	defer mu.Unlock()

	refCount++
	if refCount == 0 {
		// This is virtually impossible to happen. One will run out of memory before this happens.
		// It means the ref count wrapped around and thus we can't handle another instance. Fail.
		refCount--
		return nil, errors.E("Dial", errors.Str("user gcp: internal error: refCount wrapped around"))
	}

	this := *u                    // Clone ourselves.
	this.context = context.Copy() // Make a copy of the context, to prevent changes.
	this.endpoint = e
	return &this, nil
}

// Ping implements upspin.Service.
func (u *key) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (u *key) Close() {
	mu.Lock()
	defer mu.Unlock()

	// Clean up this instance
	u.context.SetUserName("") // ensure we get an error in subsequent calls.

	refCount--
	if refCount == 0 {
		if u.cloudClient != nil {
			u.cloudClient.Close()
		}
		u.cloudClient = nil
		// Do any other global clean ups here.
	}
}

// Authenticate implements upspin.Service.
func (u *key) Authenticate(upspin.Context) error {
	// Authentication is not dealt here. It happens at other layers.
	return nil
}

// Endpoint implements upspin.Service.
func (u *key) Endpoint() upspin.Endpoint {
	return u.endpoint
}

func init() {
	bind.RegisterKeyServer(upspin.GCP, &key{})
}
