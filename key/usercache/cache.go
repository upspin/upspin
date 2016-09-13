// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package usercache provides a KeyServer implementation that wraps
// another and caches Lookups.
// If a Lookup is made for the user that last Dialed the service,
// data from that user's context will be provided instead of making
// a request to the underlying server.
// The caching KeyServer will defer Dialing the underlying service
// until a Lookup or Put request needs to access that service.
package usercache

import (
	"sync"
	"time"

	"upspin.io/cache"
	"upspin.io/errors"
	"upspin.io/upspin"
)

type entry struct {
	expires time.Time // when the information expires.
	user    *upspin.User
}

type userCacheServer struct {
	cache *userCache

	// TODO(adg): should this plumb through to the underlying key server?
	// What would that mean?
	upspin.NoConfiguration

	// The underlying key server.
	base upspin.KeyServer

	// The following fields are used to defer dialing the underlying
	// service until a Lookup or Put call requires it.
	// If dialContext is non-nil, then the Dial method has been called.
	// If dialed is non-nil, then the underlying service has been dialed.
	mu           sync.Mutex
	dialContext  upspin.Context
	dialEndpoint upspin.Endpoint
	dialed       upspin.KeyServer
}

var _ upspin.KeyServer = (*userCacheServer)(nil)

type userCache struct {
	entries  *cache.LRU
	duration time.Duration
}

const (
	// defaultDuration is the default entry expiration time.
	defaultDuration = 15 * time.Minute

	// contextUserDuration is the expiration time of the dialing user's
	// pre-populated record. This is set to a decade to ensure that we
	// always use the context's values, unless overridden by a Put.
	contextUserDuration = 3650 * 24 * time.Hour
)

var globalCache = userCache{entries: cache.NewLRU(256), duration: defaultDuration}

// Global returns the provided key server wrapped in a global user cache.
func Global(s upspin.KeyServer) upspin.KeyServer {
	return &userCacheServer{
		base:  s,
		cache: &globalCache,
	}
}

// ResetGlobal resets the global cache.
func ResetGlobal() {
	globalCache.entries = cache.NewLRU(256)
}

// Lookup implements upspin.KeyServer.
func (c *userCacheServer) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "key/usercache.Lookup"

	// If we have an unexpired cache entry, use it.
	if v, ok := c.cache.entries.Get(name); ok {
		if !time.Now().After(v.(*entry).expires) {
			e := v.(*entry)
			return e.user, nil
		}
		c.cache.entries.Remove(name)
	}

	// Not found, look it up.
	if err := c.dial(); err != nil {
		return nil, errors.E(op, err)
	}
	u, err := c.dialed.Lookup(name)
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
func (c *userCacheServer) Put(user *upspin.User) error {
	const op = "key/usercache.Put"
	// TODO(adg): what if the user being Put is the one in dialContext?
	if err := c.dial(); err != nil {
		return errors.E(op, err)
	}
	if err := c.dialed.Put(user); err != nil {
		return errors.E(op, err)
	}
	c.cache.entries.Remove(user.Name)
	return nil
}

// Endpoint implements upspin.Service.
func (c *userCacheServer) Endpoint() upspin.Endpoint {
	// We don't want Endpoint to trigger a Dial.
	// Just return the Endpoint for either the dialed or base service.
	c.mu.Lock()
	svc := c.dialed
	c.mu.Unlock()
	if svc == nil {
		return svc.Endpoint()
	}
	return c.base.Endpoint()
}

// Ping implements upspin.Service.
func (c *userCacheServer) Ping() bool {
	// We don't want Ping to trigger a Dial.
	// If we're not yet dialed, just return true.
	c.mu.Lock()
	svc := c.dialed
	c.mu.Unlock()
	if svc == nil {
		return true
	}
	return svc.Ping()
}

// Authenticate implements upspin.Service.
func (c *userCacheServer) Authenticate(upspin.Context) error {
	return errors.Str("key/usercache.Authenticate: not implemented")
}

// Close implements upspin.Service.
func (c *userCacheServer) Close() {
	// If we're dialed, closed the dialed service.
	c.mu.Lock()
	svc := c.dialed
	c.mu.Unlock()
	if svc != nil {
		svc.Close()
		return
	}

	// Otherwise, close the underlying service.
	c.base.Close()
}

// Dial implements upspin.Dialer.
func (c *userCacheServer) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	// Put the dialed user in the cache with an extra-long expiry time
	// so that we don't hit the underlying cache for the current user and
	// instead use the values from their context.
	if ctx != nil {
		if f := ctx.Factotum(); f != nil {
			name := ctx.UserName()
			c.cache.entries.Add(name, &entry{
				expires: time.Now().Add(contextUserDuration),
				user: &upspin.User{
					Name: name,
					Dirs: []upspin.Endpoint{
						ctx.DirEndpoint(),
					},
					Stores: []upspin.Endpoint{
						ctx.StoreEndpoint(),
					},
					PublicKey: f.PublicKey(),
				},
			})
		}
	}

	cc := *c
	cc.mu = sync.Mutex{}
	cc.dialed = nil
	cc.dialContext = ctx
	cc.dialEndpoint = e
	return &cc, nil
}

// dial dials the underlying key service using the arguments
// provided to the previous invocation of Dial.
// If Dial was not called, it returns an error.
// If there is already a dialed service, it does nothing.
func (c *userCacheServer) dial() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.dialed != nil {
		return nil
	}
	if c.dialContext == nil {
		return errors.Str("server not dialed")
	}

	svc, err := c.base.Dial(c.dialContext, c.dialEndpoint)
	if err != nil {
		return err
	}
	c.dialed = svc.(upspin.KeyServer)
	return nil
}
