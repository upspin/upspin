// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcp implements the user service upspin.User on the Google Cloud Platform (GCP).
package gcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"upspin.io/bind"
	gcpCloud "upspin.io/cloud/gcp"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// Configuration options for this package.
const (
	// ConfigProjectID specifies which GCP project to use for talking to GCP.
	// If not specified, "upspin" is used.
	ConfigProjectID = "gcpProjectId"

	// ConfigBucketName specifies which GCS bucket to store data in.
	// If not specified, "g-upspin-store" is used.
	ConfigBucketName = "gcpBucketName"
)

// user is the implementation of the User Service on GCP.
type user struct {
	context     upspin.Context
	endpoint    upspin.Endpoint
	cloudClient gcpCloud.GCP
}

var _ upspin.User = (*user)(nil)

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
	errKeyTooShort     = errors.New("key length too short")
	errInvalidUserName = errors.New("invalid user name format")
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
func (u *user) AddKey(userName upspin.UserName, key upspin.PublicKey) error {
	// Validate user name
	_, err := path.Parse(upspin.PathName(userName) + "/")
	if err != nil {
		return errInvalidUserName
	}
	if len(key) < minKeyLen {
		return errKeyTooShort
	}

	// Appends to the current user entry, if any.
	ue, err := u.fetchUserEntry(userName)
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
		err = u.putUserEntry(userName, ue)
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
func (u *user) AddRoot(userName upspin.UserName, endpoint upspin.Endpoint) error {
	// Validate user name
	_, err := path.Parse(upspin.PathName(userName) + "/")
	if err != nil {
		return errInvalidUserName
	}

	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(userName)
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
	err = u.putUserEntry(userName, ue)
	if err != nil {
		return err
	}
	log.Printf("Added root %v for user %v", endpoint, userName)
	return nil
}

// Lookup implements upspin.User.
func (u *user) Lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	// Validate user name
	_, err := path.Parse(upspin.PathName(userName) + "/")
	if err != nil {
		return nil, nil, errInvalidUserName
	}
	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(userName)
	if err != nil {
		return nil, nil, err
	}
	return ue.Endpoints, ue.Keys, nil
}

// fetchUserEntry reads the user entry for a given user from permanent storage on GCP.
func (u *user) fetchUserEntry(userName upspin.UserName) (*userEntry, error) {
	// Get the user entry from GCP
	buf, err := u.cloudClient.Download(string(userName))
	if err != nil {
		log.Printf("Error downloading: %s", err)
		return nil, err
	}
	// Now convert it to a userEntry
	var ue userEntry
	err = json.Unmarshal(buf, &ue)
	if err != nil {
		return nil, err
	}
	log.Printf("Fetched user entry for %s", userName)
	return &ue, nil
}

// putUserEntry writes the user entry for a user to permanent storage on GCP.
func (u *user) putUserEntry(userName upspin.UserName, userEntry *userEntry) error {
	if userEntry == nil {
		return errors.New("nil userEntry")
	}
	jsonBuf, err := json.Marshal(userEntry)
	if err != nil {
		return fmt.Errorf("conversion to JSON failed: %v", err)
	}
	_, err = u.cloudClient.Put(string(userName), jsonBuf)
	return err
}

// Configure configures an instance of the user service.
// Required configuration options are listed at the package comments.
func (u *user) Configure(options ...string) error {
	// These are defaults that only make sense for those running upspin.io.
	bucketName := "g-upspin-store"
	projectID := "upspin"
	for _, option := range options {
		opts := strings.Split(option, "=")
		if len(opts) != 2 {
			return fmt.Errorf("invalid option format: %q", option)
		}
		switch opts[0] {
		case ConfigBucketName:
			bucketName = opts[1]
		case ConfigProjectID:
			projectID = opts[1]
		default:
			return fmt.Errorf("invalid configuration option: %q", opts[0])
		}
	}

	u.cloudClient = gcpCloud.New(projectID, bucketName, gcpCloud.BucketOwnerFullCtrl)
	log.Debug.Printf("Configured GCP user: %v", options)
	return nil
}

// isServiceConfigured reports whether the user service has been configured via a Configure call.
func (u *user) isServiceConfigured() bool {
	return u.cloudClient != nil && u.context.UserName != ""
}

// Dial implements upspin.Service.
func (u *user) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	if e.Transport != upspin.GCP {
		return nil, errors.New("user gcp: unrecognized transport")
	}
	mu.Lock()
	defer mu.Unlock()

	refCount++
	if refCount == 0 {
		// This is virtually impossible to happen. One will run out of memory before this happens.
		// It means the ref count wrapped around and thus we can't handle another instance. Fail.
		refCount--
		return nil, errors.New("user gcp: internal error: refCount wrapped around")
	}

	this := *u              // Clone ourselves.
	this.context = *context // Make a copy of the context, to prevent changes.
	this.endpoint = e
	return &this, nil
}

// Ping implements upspin.Service.
func (u *user) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (u *user) Close() {
	mu.Lock()
	defer mu.Unlock()

	// Clean up this instance
	u.context.UserName = "" // ensure we get an error in subsequent calls.

	refCount--
	if refCount == 0 {
		u.cloudClient = nil
		// Do any other global clean ups here.
	}
}

// Authenticate implements upspin.Service.
func (u *user) Authenticate(*upspin.Context) error {
	// Authentication is not dealt here. It happens at other layers.
	return nil
}

// ServerUserName implements upspin.Service.
func (u *user) ServerUserName() string {
	return string(u.context.UserName)
}

// Endpoint implements upspin.Service.
func (u *user) Endpoint() upspin.Endpoint {
	return u.endpoint
}

func init() {
	bind.RegisterUser(upspin.GCP, &user{})
}
