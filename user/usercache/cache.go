// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package usercache pushes a user cache in front of context.User.
package usercache

import (
	"fmt"
	"time"

	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/upspin"
)

type entry struct {
	expires time.Time // when the information expires.
	eps     []upspin.Endpoint
	pub     []upspin.PublicKey
}

type userCache struct {
	userEndpoint   upspin.Endpoint
	context        upspin.Context
	serverUserName string
	entries        *cache.LRU
	duration       time.Duration
}

// New creates a cache onto the User service.  After this all User service requests will
// be filtered through the cache.
//
// TODO(p): New is not concurrency safe since context is assumed to be immutable
// everywhere else.  Not sure this needs to be fixed but should at least be noted.
func New(context *upspin.Context) upspin.User {
	return &userCache{
		context:      *context, // make a copy.
		userEndpoint: context.User,
		entries:      cache.NewLRU(256),
		duration:     time.Minute * 15,
	}
}

// Lookup implements upspin.User.Lookup.
func (c *userCache) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	v, ok := c.entries.Get(name)

	// If we have an unexpired binding, use it.
	if ok {
		if !time.Now().After(v.(*entry).expires) {
			e := v.(*entry)
			return e.eps, e.pub, nil
		}
		c.entries.Remove(name)
	}

	// Not found, look it up.
	user, err := bind.User(&c.context, c.userEndpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("usercache: error binding to User service on %v for user %q: %s",
			c.userEndpoint, c.context.UserName, err.Error())
	}
	defer bind.Release(user)
	eps, pub, err := user.Lookup(name)
	if err != nil {
		return nil, nil, err
	}
	e := &entry{
		expires: time.Now().Add(c.duration),
		eps:     eps,
		pub:     pub,
	}
	c.entries.Add(name, e)
	return eps, pub, nil
}

// Dial implements upspin.User.Dial.
func (c *userCache) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return c, nil
}

// Configure implements upspin.Service.
func (c *userCache) Configure(options ...string) error {
	panic("unimplemented")
}

// Endpoint implements upspin.Service.
func (c *userCache) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}

// Ping implements upspin.Service.
func (c *userCache) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (c *userCache) Close() {
	c.entries = nil
}

// Authenticate implements upspin.Service.
func (c *userCache) Authenticate(*upspin.Context) error {
	return nil
}

// SetDuration sets the duration until entries expire.  Primarily
// intended for testing.
func (c *userCache) SetDuration(d time.Duration) {
	c.duration = d
}
