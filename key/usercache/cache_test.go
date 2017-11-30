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
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/test/testutil"
	"upspin.io/upspin"
)

// service is a KeyServer implementation that counts lookups.
type service struct {
	lookups int
	dials   int
	entries map[string]*upspin.User

	config   upspin.Config
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

var (
	testDirEndpoint   = upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "dir"}
	testStoreEndpoint = upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "store"}
	testPublicKey     upspin.PublicKey
)

// setup returns configs with the KeyServer uncached and cached.
func setup(t *testing.T, user string) (uncached, cached upspin.KeyServer) {
	c := config.New()
	c = config.SetUserName(c, upspin.UserName(user))
	c = config.SetPacking(c, upspin.PlainPack)
	c = config.SetKeyEndpoint(c, keyService.endpoint)
	keyService.config = c

	cache := &userCacheServer{
		base: keyService,
		cache: &userCache{
			entries:  cache.NewLRU(256),
			duration: 1 * time.Second,
		},
	}

	if user == "test@upspin.io" {
		c = config.SetDirEndpoint(c, testDirEndpoint)
		c = config.SetStoreEndpoint(c, testStoreEndpoint)
		f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "test"))
		if err != nil {
			t.Fatal(err)
		}
		c = config.SetFactotum(c, f)
		testPublicKey = f.PublicKey()
	}

	svc, err := cache.Dial(c, keyService.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return keyService, svc.(upspin.KeyServer)
}

// TestDial tests that we don't Dial the underlying
// service if the Lookup is for the user in the
// Dialed config, and that we dial just once for
// Lookups of other users.
func TestDial(t *testing.T) {
	const name = "test@upspin.io"
	_, svc := setup(t, name)

	keyService.dials = 0

	// Try asking for the user in the dialed config.
	// We should get it back without doing any actual dials.
	got, err := svc.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	if n := keyService.dials; n != 0 {
		t.Fatalf("underlying key service dialed %d times, want 0", n)
	}
	want := &upspin.User{
		Name:      name,
		Dirs:      []upspin.Endpoint{testDirEndpoint},
		Stores:    []upspin.Endpoint{testStoreEndpoint},
		PublicKey: testPublicKey,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup(%q) returned %#v, want %#v", name, got, want)
	}

	// While asking for other users should do a single dial.
	if _, err = svc.Lookup("a@a.com"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Lookup("b@b.com"); err != nil {
		t.Fatal(err)
	}
	if n := keyService.dials; n != 1 {
		t.Fatalf("underlying key service dialed %d times, want 1", n)
	}

	// Asking for the config user again should still return the same data.
	got, err = svc.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lookup(%q) returned %#v, want %#v", name, got, want)
	}
}

// TestPut tests that a Put through the cache reaches the
// underlying cache and invalidates the cache.
func TestPut(t *testing.T) {
	const name = "user@example.com"
	keyService.add(name)

	uncached, svc := setup(t, name)

	u, err := svc.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}

	want := *u
	want.Dirs = []upspin.Endpoint{testDirEndpoint}
	want.Stores = []upspin.Endpoint{testStoreEndpoint}

	if err := svc.Put(&want); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("Lookup(%q) returned %#v, want %#v", name, *got, want)
	}
	got, err = uncached.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("Lookup(%q) returned %#v, want %#v", name, *got, want)
	}
}

// TestCache tests the User cache for equivalence with the uncached version and
// for efficacy of the cached version.
func TestCache(t *testing.T) {
	unc, c := setup(t, "TestCache@nowhere.com")

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
	unc, c := setup(t, "TestExpiration@nowhere.com")

	// Cache the 4 names.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")
	sofar := keyService.lookups

	time.Sleep(2 * time.Second) // expiry is one second

	// After a few seconds all entries should expire.
	try(t, unc, c, "a@a.com")
	try(t, unc, c, "b@b.com")
	try(t, unc, c, "c@c.com")
	try(t, unc, c, "d@d.com")
	if keyService.lookups != sofar+2*4 {
		t.Errorf("uncached loookups, got %d, expected %d", keyService.lookups, sofar+2*4)
	}
}

func TestEndpoint(t *testing.T) {
	const name = "test@upspin.io"
	_, svc := setup(t, name)

	if got := svc.Endpoint(); got != keyService.Endpoint() {
		t.Errorf("endpoint = %v, want %v", got, keyService.Endpoint())
	}
}

// try looks up a name through the cached and uncached KeyServers and
// compares the results.
func try(t *testing.T, uncached, cached upspin.KeyServer, name string) {
	su, serr := uncached.Lookup(upspin.UserName(name))
	cu, cerr := cached.Lookup(upspin.UserName(name))

	if !reflect.DeepEqual(serr, cerr) {
		t.Fatalf("for %s got %v expect %v", name, cerr, serr)
	}
	if !reflect.DeepEqual(su.Dirs, cu.Dirs) {
		t.Errorf("for %s got %v expect %v", name, cu.Dirs, su.Dirs)
	}
	if cu.PublicKey != su.PublicKey {
		t.Errorf("for %s got %q expect %q", name, cu.PublicKey, su.PublicKey)
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
	const op errors.Op = "key/usercache.service.Lookup"
	s.lookups++
	if u, ok := s.entries[string(name)]; ok {
		u2 := *u // Copy to avoid problems.
		return &u2, nil
	}
	return nil, errors.E(op, name, errors.NotExist)
}

func (s *service) Put(user *upspin.User) error {
	u := *user // Copy to avoid problems.
	s.entries[string(user.Name)] = &u
	return nil
}

func (s *service) Dial(cfg upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	s.dials++
	s.config = cfg
	s.endpoint = e
	return s, nil
}

func (s *service) Endpoint() upspin.Endpoint {
	return s.endpoint
}

func (s *service) Close() {
}
