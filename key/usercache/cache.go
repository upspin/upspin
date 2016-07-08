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
	"upspin.io/upspin"
)

type entry struct {
	expires time.Time // when the information expires.
	eps     []upspin.Endpoint
	pub     []upspin.PublicKey
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
func (c *userCacheContext) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	v, ok := c.cache.entries.Get(name)

	// If we have an unexpired binding, use it.
	if ok {
		if !time.Now().After(v.(*entry).expires) {
			e := v.(*entry)
			return e.eps, e.pub, nil
		}
		c.cache.entries.Remove(name)
	}

	// Not found, look it up.
	eps, pub, err := c.Context.KeyServer().Lookup(name)
	if err != nil {
		return nil, nil, err
	}
	e := &entry{
		expires: time.Now().Add(c.cache.duration),
		eps:     eps,
		pub:     pub,
	}
	c.cache.entries.Add(name, e)
	return eps, pub, nil
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
func (ctx *userCacheContext) KeyServer() upspin.KeyServer {
	return ctx
}

// SetUserName implements upspin.Context.
func (ctx *userCacheContext) SetUserName(u upspin.UserName) upspin.Context {
	ctx.Context.SetUserName(u)
	return ctx
}

// SetFactotum implements upspin.Context.
func (ctx *userCacheContext) SetFactotum(f upspin.Factotum) upspin.Context {
	ctx.Context.SetFactotum(f)
	return ctx
}

// Packing implements upspin.Context.
func (ctx *userCacheContext) Packing() upspin.Packing {
	return ctx.Context.Packing()
}

// SetPacking implements upspin.Context.
func (ctx *userCacheContext) SetPacking(p upspin.Packing) upspin.Context {
	ctx.Context.SetPacking(p)
	return ctx
}

// SetKeyEndpoint implements upspin.Context.
func (ctx *userCacheContext) SetKeyEndpoint(e upspin.Endpoint) upspin.Context {
	ctx.Context.SetKeyEndpoint(e)
	return ctx
}

// SetDirEndpoint implements upspin.Context.
func (ctx *userCacheContext) SetDirEndpoint(e upspin.Endpoint) upspin.Context {
	ctx.Context.SetDirEndpoint(e)
	return ctx
}

// StoreEndpoint implements upspin.Context.
func (ctx *userCacheContext) StoreEndpoint() upspin.Endpoint {
	return ctx.Context.StoreEndpoint()
}

// SetStoreEndpoint implements upspin.Context.
func (ctx *userCacheContext) SetStoreEndpoint(e upspin.Endpoint) upspin.Context {
	ctx.Context.SetStoreEndpoint(e)
	return ctx
}

// Copy implements upspin.Context. We are actually copying the underlying context but
// still pointing to the same LRU cache.
func (ctx *userCacheContext) Copy() upspin.Context {
	c := *ctx
	c.Context = ctx.Context.Copy()
	return &c
}
