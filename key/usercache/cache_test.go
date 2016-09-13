// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package usercache

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// service is a KeyServer implementation that counts lookups.
type service struct {
	lookups int
	entries map[string]*upspin.User

	context  upspin.Context
	endpoint upspin.Endpoint
}

var keyService = &service{
	entries:  make(map[string]*upspin.User),
	endpoint: upspin.Endpoint{Transport: upspin.InProcess},
}

func init() {
	keyService.add("a@a.com")
	keyService.add("b@b.com")
	keyService.add("c@c.com")
	keyService.add("d@d.com")

	err := bind.RegisterKeyServer(keyService.endpoint.Transport, keyService)
	if err != nil {
		panic(err)
	}
}

// setup returns contexts with the KeyServer uncached and cached.
func setup(t *testing.T, d time.Duration, user string) (uncached, cached upspin.KeyServer) {
	c := context.New()
	c = context.SetUserName(c, upspin.UserName(user))
	c = context.SetPacking(c, upspin.DebugPack)
	c = context.SetKeyEndpoint(c, keyService.endpoint)
	keyService.context = c

	cache := &userCacheServer{
		KeyServer: keyService,
		cache: &userCache{
			entries:  cache.NewLRU(256),
			duration: d,
		},
	}
	return keyService, cache
}

// TestCache tests the User cache for equivalence with the uncached version and
// for efficacy of the cached version.
func TestCache(t *testing.T) {
	unc, c := setup(t, 10*time.Second, "TestCache@nowhere.com")

	// Compare the 4 names twixt cached and uncached.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")

	sofar := keyService.lookups

	// Check for consistency between cached and uncached.
	loops := 200
	for i := 0; i < loops; i++ {
		try(t, unc, c, "a@a.com")
		try(t, unc, c, "b@b.com")
		try(t, unc, c, "c@c.com")
		try(t, unc, c, "d@d.com")
	}

	// If the cache worked, we should only have 1 uncached access per try() in the loop.
	if keyService.lookups != sofar+4*loops {
		t.Errorf("uncached loookups, got %d, expected %d", keyService.lookups, sofar+4*loops)
	}
}

// TestExpiration tests that cache entries time out.
func TestExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Expiration tests skipped in short mode")
	}
	unc, c := setup(t, time.Second, "TestExpiration@nowhere.com")

	// Cache the 4 names.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")
	sofar := keyService.lookups

	time.Sleep(2 * time.Second)

	// After a few seconds all entries should expire.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")
	if keyService.lookups != sofar+2*4 {
		t.Errorf("uncached loookups, got %d, expected %d", keyService.lookups, sofar+2*4)
	}
}

// try looks up a name through the cached and uncached KeyServers and
// compares the results.
func try(t *testing.T, uncached, cached upspin.KeyServer, name string) {
	su, serr := uncached.Lookup(upspin.UserName(name))
	cu, cerr := cached.Lookup(upspin.UserName(name))

	if !reflect.DeepEqual(su.Dirs, cu.Dirs) {
		t.Errorf("for %s got %v expect %v", name, cu.Dirs, su.Dirs)
	}
	if cu.PublicKey != su.PublicKey {
		t.Errorf("for %s got %q expect %q", name, cu.PublicKey, su.PublicKey)
	}
	if !reflect.DeepEqual(serr, cerr) {
		t.Errorf("for %s got %v expect %v", name, cerr, serr)
	}
}

func (s *service) add(name string) {
	u := &upspin.User{
		Name: upspin.UserName(name),
	}
	for i := 0; i < 3; i++ {
		ep := upspin.Endpoint{
			Transport: upspin.InProcess,
			NetAddr:   upspin.NetAddr(fmt.Sprintf("%s%d", name, i)),
		}
		u.Dirs = append(u.Dirs, ep)
	}
	u.PublicKey = upspin.PublicKey(name + ".key")

	s.entries[name] = u
}

func (s *service) Lookup(name upspin.UserName) (*upspin.User, error) {
	const op = "key/usercache.service.Lookup"
	s.lookups++
	if u, ok := s.entries[string(name)]; ok {
		return u, nil
	}
	return nil, errors.E(op, name, errors.NotExist)
}

func (s *service) Put(user *upspin.User) error {
	panic("userCacheTest.Service.Put not implemented")
}

func (s *service) Dial(ctx upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	s.context = ctx
	s.endpoint = e
	return s, nil
}

func (s *service) Endpoint() upspin.Endpoint {
	return s.endpoint
}

func (s *service) Configure(options ...string) (upspin.UserName, error) {
	return "", nil
}

func (s *service) Ping() bool {
	return true
}

func (s *service) Close() {
}
