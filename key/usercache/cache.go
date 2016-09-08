// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package usercache provides a caching keyserver implementation.
// It passes all operations except Lookup to the underlying keyserver.
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

type userCacheServer struct {
	upspin.KeyServer
	cache *userCache
}

var _ upspin.KeyServer = (*userCacheServer)(nil)

type userCache struct {
	entries  *cache.LRU
	duration time.Duration
}

const defaultDuration = 15 * time.Minute

var globalCache = userCache{entries: cache.NewLRU(256), duration: defaultDuration}

// Global returns the provided key server wrapped in a global user cache.
func Global(s upspin.KeyServer) upspin.KeyServer {
	return &userCacheServer{
		KeyServer: s,
		cache:     &globalCache,
	}
}

// ResetGlobal resets the global cache.
func ResetGlobal() {
	globalCache.entries = cache.NewLRU(256)
}

// Lookup implements upspin.KeyServer.
func (c *userCacheServer) Lookup(name upspin.UserName) (*upspin.User, error) {
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
	u, err := c.KeyServer.Lookup(name)
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

// Dial implements upspin.Dialer.
func (c *userCacheServer) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "key/usercache.Dial"
	svc, err := c.KeyServer.Dial(ctx, e)
	if err != nil {
		return nil, errors.E(op, err)
	}
	return &userCacheServer{svc.(upspin.KeyServer), c.cache}, nil
}
