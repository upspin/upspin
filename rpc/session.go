// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"time"

	"upspin.io/cache"
	"upspin.io/upspin"
)

// Session contains information about the connection and the authenticated user, if any.
type Session interface {
	// User returns the user name present in the session. It may be empty. Note: user, even when present,
	// might not be authenticated.
	User() upspin.UserName

	// Err reports the status of the session.
	Err() error

	// Expires returns the expiration time of the session, in UTC.
	Expires() time.Time

	// ProxiedEndpoint returns the endpoint for which this session is a proxy.
	// If we aren't proxying it returns an Unassigned endpoint.
	ProxiedEndpoint() upspin.Endpoint
}

// sessionCacheSize is the max number of sessions to remember. Small values will limit parallelism and
// very large values will allow authToken collisions, either accidentally or by brute-force attacks.
const sessionCacheSize = 1000

var sessionCache *cache.LRU // Caches <authToken, Session> Thread safe.

// NewSession creates a new session with the given contents.
func NewSession(user upspin.UserName, expiration time.Time, authToken string, proxyFor *upspin.Endpoint, err error) Session {
	session := &sessionImpl{
		user:      user,
		expires:   expiration,
		authToken: authToken,
		err:       err,
		endpoint:  *proxyFor,
	}
	sessionCache.Add(authToken, session)
	return session
}

// GetSession returns a session associated with an auth token, if one has been cached or nil otherwise.
func GetSession(authToken string) Session {
	session, ok := sessionCache.Get(authToken)
	if !ok {
		return nil
	}
	return session.(Session)
}

// ClearSession removes a session associated with a given auth token from the cache.
func ClearSession(authToken string) {
	sessionCache.Remove(authToken)
}

// resetSessions creates a new session cache. It is not thread safe. To be used for testing only.
func resetSessions() {
	sessionCache = cache.NewLRU(sessionCacheSize)
}

type sessionImpl struct {
	user      upspin.UserName
	authToken string
	err       error
	expires   time.Time
	endpoint  upspin.Endpoint
}

var _ Session = (*sessionImpl)(nil)

// User implements Session.
func (s *sessionImpl) User() upspin.UserName {
	return s.user
}

// Err implements Session.
func (s *sessionImpl) Err() error {
	return s.err
}

// Expires implements Session.
func (s *sessionImpl) Expires() time.Time {
	return s.expires
}

// ProxiedEndpoint implements Session.
func (s *sessionImpl) ProxiedEndpoint() upspin.Endpoint {
	return s.endpoint
}

func init() {
	resetSessions()
}
