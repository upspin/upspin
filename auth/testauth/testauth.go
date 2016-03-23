// Package testauth contains helper functions and objects to test auth servers.
package testauth

import (
	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/upspin"
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

// User implements auth.Handler.
func (ds *dummySession) User() upspin.UserName {
	return ds.user
}

// IsAuthenticated implements auth.Handler.
func (ds *dummySession) IsAuthenticated() bool {
	return ds.isAuth
}

// Err implements auth.Handler.
func (ds *dummySession) Err() error {
	return ds.err
}
