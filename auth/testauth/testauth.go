// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testauth contains helper functions and objects to test auth servers.
package testauth

import (
	"time"

	"upspin.io/auth"
	"upspin.io/upspin"
)

type dummySession struct {
	user   upspin.UserName
	isAuth bool
	err    error
}

// NewSessionForTesting returns a new session initialized with the given parameters.
func NewSessionForTesting(user upspin.UserName, isAuth bool, err error) auth.Session {
	return &dummySession{
		user:   user,
		isAuth: isAuth,
		err:    err,
	}
}

// User implements auth.Session.
func (ds *dummySession) User() upspin.UserName {
	return ds.user
}

// IsAuthenticated implements auth.Session.
func (ds *dummySession) IsAuthenticated() bool {
	return ds.isAuth
}

// Err implements auth.Session
func (ds *dummySession) Err() error {
	return ds.err
}

// Expires implements auth.Session.
func (ds *dummySession) Expires() time.Time {
	return time.Now().Add(100 * time.Hour) // TODO: not used yet.
}
