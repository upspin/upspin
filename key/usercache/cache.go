// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package usercache pushes a new Context onto an old. It passes all operations except KeyServer
// to the underlying context. KeyServer returns a pointer to a cached version of the underlying
// context's KeyServer.
package usercache

import (
	"time"

	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/upspin"
)

type entry struct {
	expires time.Time // when the information expires.
	user    *upspin.User
}

type userCacheContext struct {
	upspin.Context
	cache *userCache
}

type userCache struct {
	entries  *cache.LRU
	duration time.Duration
}

const defaultDuration = 15 * time.Minute

var globalCache = userCache{entries: cache.NewLRU(256), duration: defaultDuration}

// Private pushes a new user cache onto a context. If duration is non-zero
// it specifies the lifetime of cache entries.
func Private(context upspin.Context, duration time.Duration) upspin.Context {
	if duration == 0 {
		duration = defaultDuration
	}
	return &userCacheContext{
		Context: context,
		cache: &userCache{
			entries:  cache.NewLRU(256),
			duration: duration,
		},
	}
}

// Global pushes a global user cache onto a context.
func Global(context upspin.Context) upspin.Context {
	if c, ok := context.(*userCacheContext); ok {
		if c.cache == &globalCache {
			return c
		}
	}
	return &userCacheContext{
		Context: context,
		cache:   &globalCache,
	}
}

// ResetGlobal resets the global cache.
func ResetGlobal() {
	globalCache.entries = cache.NewLRU(256)
}

// Lookup implements upspin.KeyServer.
func (c *userCacheContext) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "key/usercache.Lookup"
	v, ok := c.cache.entries.Get(name)

	// If we have an unexpired binding, use it.
	if ok {
		if !time.Now().After(v.(*entry).expires) {
			e := v.(*entry)
			return e.user, nil
		}
		c.cache.entries.Remove(name)
	}

	// Not found, look it up.
	u, err := c.Context.KeyServer().Lookup(name)
	if err != nil {
		return nil, errors.E(op, err)
	}
	e := &entry{
		expires: time.Now().Add(c.cache.duration),
		user:    u,
	}
	c.cache.entries.Add(name, e)
	return u, nil
}

// Put implements upspin.KeyServer.
func (c *userCacheContext) Put(user *upspin.User) error {
	const op = "key/usercache.Put"
	return errors.E(op, errors.Syntax, errors.Str("not implemented"))
}

// Dial implements upspin.Service.
func (c *userCacheContext) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return c, nil
}

// Configure implements upspin.Service.
func (c *userCacheContext) Configure(options ...string) (upspin.UserName, error) {
	panic("unimplemented")
}

// Endpoint implements upspin.Service.
func (c *userCacheContext) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}

// Ping implements upspin.Service.
func (c *userCacheContext) Ping() bool {
	return true
}

// Close implements upspin.Service.
func (c *userCacheContext) Close() {
	c.cache.entries = nil
}

// Authenticate implements upspin.Service.
func (c *userCacheContext) Authenticate(upspin.Context) error {
	return nil
}

// KeyServer implements upspin.Context. It returns a pointer to the caching user service.
func (c *userCacheContext) KeyServer() upspin.KeyServer {
	return c
}
