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
		Context: context.Copy(),
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
		Context: context.Copy(),
		cache:   &globalCache,
	}
}

// Lookup implements upspin.KeyServer.Lookup.
func (c *userCacheContext) Lookup(name upspin.UserName) (*upspin.User, error) {
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
		return nil, err
	}
	e := &entry{
		expires: time.Now().Add(c.cache.duration),
		user:    u,
	}
	c.cache.entries.Add(name, e)
	return u, nil
}

// Put implements upspin.KeyServer.Put.
func (c *userCacheContext) Put(user *upspin.User) error {
	return errors.E("Put", errors.Syntax, errors.Str("not implemented"))
}

// Dial implements upspin.Service.
func (c *userCacheContext) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return c, nil
}

// Configure implements upspin.Service.
func (c *userCacheContext) Configure(options ...string) error {
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

// SetUserName implements upspin.Context.
func (c *userCacheContext) SetUserName(u upspin.UserName) upspin.Context {
	c.Context.SetUserName(u)
	return c
}

// SetFactotum implements upspin.Context.
func (c *userCacheContext) SetFactotum(f upspin.Factotum) upspin.Context {
	c.Context.SetFactotum(f)
	return c
}

// Packing implements upspin.Context.
func (c *userCacheContext) Packing() upspin.Packing {
	return c.Context.Packing()
}

// SetPacking implements upspin.Context.
func (c *userCacheContext) SetPacking(p upspin.Packing) upspin.Context {
	c.Context.SetPacking(p)
	return c
}

// SetKeyEndpoint implements upspin.Context.
func (c *userCacheContext) SetKeyEndpoint(e upspin.Endpoint) upspin.Context {
	c.Context.SetKeyEndpoint(e)
	return c
}

// SetDirEndpoint implements upspin.Context.
func (c *userCacheContext) SetDirEndpoint(e upspin.Endpoint) upspin.Context {
	c.Context.SetDirEndpoint(e)
	return c
}

// StoreEndpoint implements upspin.Context.
func (c *userCacheContext) StoreEndpoint() upspin.Endpoint {
	return c.Context.StoreEndpoint()
}

// SetStoreEndpoint implements upspin.Context.
func (c *userCacheContext) SetStoreEndpoint(e upspin.Endpoint) upspin.Context {
	c.Context.SetStoreEndpoint(e)
	return c
}

// Copy implements upspin.Context. We are actually copying the underlying context but
// still pointing to the same LRU cache.
func (c *userCacheContext) Copy() upspin.Context {
	newC := *c
	newC.Context = c.Context.Copy()
	return &newC
}
