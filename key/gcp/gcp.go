// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements the user service upspin.KeyServer on the Google Cloud Platform (GCP).
package gcp

import (
	"encoding/json"
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

// userEntry is the on-disk representation of upspin.User, further annotated with
// non-public information, such as whether the user is an admin.
type userEntry struct {
	User    upspin.User
	IsAdmin bool
}

var (
	errInvalidUserName = errors.E(errors.Invalid, errors.Str("invalid user name format"))
)

var (
	mu       sync.Mutex // protects fields below
	refCount uint64
)

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
	return &ue.User, nil
}

// Put implements upspin.KeyServer.
func (u *key) Put(user *upspin.User) error {
	const Put = "Key.Put"
	// Validate the user name in user.
	_, err := path.Parse(upspin.PathName(user.Name) + "/")
	if err != nil {
		return errors.E(Put, errInvalidUserName)
	}
	// Retrieve info about the user we want to Put.
	isAdmin := false
	ue, err := u.fetchUserEntry(Put, user.Name)
	if err != nil {
		if err.(*errors.Error).Kind != errors.NotExist {
			return err
		}
	} else {
		isAdmin = ue.IsAdmin
	}

	// Is the user operating on his/her own record?
	if user.Name != u.context.UserName() {
		// Not operating on own record, so we need to ensure u.context.UserName is an admin.
		// First, retrieve the user entry for the context user.
		ue, err := u.fetchUserEntry(Put, u.context.UserName())
		if err != nil {
			return err
		}
		if !ue.IsAdmin {
			return errors.E(Put, errors.Permission, errors.Str("not an administrator"))
		}
		// Is admin. Proceed.
	}
	// Put puts, it does not update, so we simply overwrite what's there if it exists.
	// Set IsAdmin to what it was before or false by default.
	return u.putUserEntry(Put, &userEntry{User: *user, IsAdmin: isAdmin})
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
func (u *key) putUserEntry(op string, userEntry *userEntry) error {
	log.Printf("PutUserEntry %v", userEntry)
	if userEntry == nil {
		return errors.E(op, errors.Invalid, errors.Str("nil userEntry"))
	}
	jsonBuf, err := json.Marshal(userEntry)
	if err != nil {
		return errors.E(op, errors.Invalid, errors.Errorf("conversion to JSON failed: %v", err))
	}
	_, err = u.cloudClient.Put(string(userEntry.User.Name), jsonBuf)
	if err != nil {
		return errors.E(op, errors.IO, err)
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
