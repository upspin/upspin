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
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/upspin"

	"strings"

	_ "upspin.io/dir/inprocess"
	_ "upspin.io/store/inprocess"
)

// service is a KeyServer implementation that counts lookups.
type service struct {
	lookups int
	entries map[string]*upspin.User

	context  upspin.Context
	endpoint upspin.Endpoint
	dialed   int
}

// setup returns contexts with the KeyServer uncached and cached.
func setup(t *testing.T, d time.Duration, user string) (upspin.Context, upspin.Context, *service) {
	c := context.New().SetUserName(upspin.UserName(user)).SetPacking(upspin.DebugPack)
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}

	s := &service{
		entries:  make(map[string]*upspin.User),
		endpoint: e,
		context:  c.Copy(),
	}
	s.add("a@a.com")
	s.add("b@b.com")
	s.add("c@c.com")
	s.add("d@d.com")

	err := bind.RegisterKeyServer(e.Transport, s)
	if err != nil {
		if strings.Contains(err.Error(), "cannot override") {
			err = bind.ReregisterKeyServer(e.Transport, s)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	c.SetKeyEndpoint(e)

	return c, Private(c, d), s
}

// TestCache tests the User cache for equivalence with the uncached version and
// for efficacy of the cached version.
func TestCache(t *testing.T) {
	unc, c, s := setup(t, 0, "TestCache@nowhere.com")

	// Compare the 4 names twixt cached and uncached.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")

	if s.dialed != 1 {
		t.Errorf("Expected 1 dial. Got %d", s.dialed)
	}

	sofar := s.lookups

	// Check for consistency between cached and uncached.
	loops := 200
	for i := 0; i < loops; i++ {
		try(t, unc, c, "a@a.com")
		try(t, unc, c, "b@b.com")
		try(t, unc, c, "c@c.com")
		try(t, unc, c, "d@d.com")
	}

	// No new cache misses, so no new dials.
	if s.dialed != 1 {
		t.Errorf("Expected no new dials, just the original 1. Got %d", s.dialed)
	}

	// If the cache worked, we should only have 1 uncached access per try() in the loop.
	if s.lookups != sofar+4*loops {
		t.Errorf("uncached loookups, got %d, expected %d", s.lookups, sofar+4*loops)
	}
}

// TestExpiration tests that cache entries time out.
func TestExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Expiration tests skipped in short mode")
	}
	unc, c, s := setup(t, time.Second, "TestExpiration@nowhere.com")

	// Cache the 4 names.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")
	sofar := s.lookups

	time.Sleep(2 * time.Second)

	// After a few seconds all entries should expire.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")
	if s.lookups != sofar+2*4 {
		t.Errorf("uncached loookups, got %d, expected %d", s.lookups, sofar+2*4)
	}

	if s.dialed != 1 {
		t.Errorf("Expected 1 dial, got %d", s.dialed)
	}
}

// try looks up a name through the cached and uncached KeyServers and
// compares the results.
func try(t *testing.T, unc upspin.Context, c upspin.Context, name string) {
	su, serr := unc.KeyServer().Lookup(upspin.UserName(name))
	cu, cerr := c.KeyServer().Lookup(upspin.UserName(name))

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
	s.lookups++
	if u, ok := s.entries[string(name)]; ok {
		return u, nil
	}
	return nil, errors.E("Lookup", name, errors.NotExist)
}

func (s *service) Put(user *upspin.User) error {
	panic("userCacheTest.Service.Put not implemented")
}

func (s *service) Dial(context upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	s.dialed++
	s.context = context.Copy()
	s.endpoint = e
	return s, nil
}

func (s *service) Endpoint() upspin.Endpoint {
	return s.endpoint
}

func (s *service) Configure(options ...string) error {
	return nil
}

func (s *service) Ping() bool {
	return true
}

func (s *service) Close() {
}

func (s *service) Authenticate(upspin.Context) error {
	return nil
}
