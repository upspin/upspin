// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bind

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	"upspin.io/test/testfixtures"
	"upspin.io/upspin"
)

func TestSwitch(t *testing.T) {
	cfg := testfixtures.NewSimpleConfig("nobody@example.com")

	// These should succeed.
	du := &dummyKey{}
	if err := RegisterKeyServer(upspin.InProcess, du); err != nil {
		t.Errorf("RegisterKeyServer failed")
	}
	if err := RegisterStoreServer(upspin.InProcess, &dummyStoreServer{}); err != nil {
		t.Errorf("RegisterStoreServer failed")
	}
	if err := RegisterDirServer(upspin.InProcess, &dummyDirServer{}); err != nil {
		t.Errorf("RegisterDirServer failed")
	}

	// These should fail.
	if err := RegisterKeyServer(upspin.InProcess, &dummyKey{}); err == nil {
		t.Errorf("RegisterKeyServer should have failed")
	}
	if err := RegisterStoreServer(upspin.InProcess, &dummyStoreServer{}); err == nil {
		t.Errorf("RegisterStoreServer should have failed")
	}
	if err := RegisterDirServer(upspin.InProcess, &dummyDirServer{}); err == nil {
		t.Errorf("RegisterDirServer should have failed")
	}

	// These should return different NetAddrs
	s1, _ := StoreServer(cfg, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr1"})
	s2, _ := StoreServer(cfg, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr2"})
	if s1.Endpoint().NetAddr != "addr1" || s2.Endpoint().NetAddr != "addr2" {
		t.Errorf("got %s %s, expected addr1 addr2", s1.Endpoint().NetAddr, s2.Endpoint().NetAddr)
	}

	// This should fail.
	if _, err := StoreServer(cfg, upspin.Endpoint{Transport: upspin.Transport(99)}); err == nil {
		t.Errorf("expected bind.StoreServer of undefined to fail")
	}

	// Test caching. dummyKey has a dial count.
	e := upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr1"}
	u1, err := KeyServer(cfg, e) // Dials once.
	if err != nil {
		t.Fatal(err)
	}
	u2, err := KeyServer(cfg, e) // Does not dial; hits the cache.
	if err != nil {
		t.Fatal(err)
	}
	if u1 != u2 {
		t.Errorf("Expected the same instance.")
	}
	if du.dialed != 1 {
		t.Errorf("Expected only one dial. Got %d", du.dialed)
	}
	// But a different config forces a new dial.
	cfg2 := testfixtures.NewSimpleConfig("bob@foo.com")
	u3, err := KeyServer(cfg2, e) // Dials again,
	if err != nil {
		t.Fatal(err)
	}
	if du.dialed != 2 {
		t.Errorf("Expected two dials. Got %d", du.dialed)
	}
	if u1.(*dummyKey).pingCount != 0 {
		t.Errorf("Expected zero pings. Got %d", du.pingCount)
	}

	// Now check that Release works.
	if len(userDialCache) != 2 {
		t.Errorf("Expected two user services in the cache, got %d", len(userDialCache))
	}

	err = Release(u1) // u2 == u1
	if err != nil {
		t.Fatal(err)
	}
	err = Release(u3)
	if err != nil {
		t.Fatal(err)
	}

	if len(userDialCache) != 0 {
		t.Errorf("Expected only no user service in the cache.")
	}

	if u1.(*dummyKey).closeCalled != 1 {
		t.Errorf("Expected close to be called once on u1")
	}
	if u3.(*dummyKey).closeCalled != 1 {
		t.Errorf("Expected close to be called once on u3")
	}
}

func TestConcurrency(t *testing.T) {
	const nRuns = 10
	pingFreshnessDuration = 0 // Forces ping to always be invalid
	defer func() { pingFreshnessDuration = 15 * time.Minute }()

	cfg := testfixtures.NewSimpleConfig("nobody@example.com")
	e := upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr17"}

	var wg sync.WaitGroup
	store := func(release bool) {
		defer wg.Done()
		for i := 0; i < nRuns; i++ {
			var s upspin.Service
			var err error
			if i&1 == 0 { // alternate between store and keyservers
				s, err = StoreServer(cfg, e)
			} else {
				s, err = KeyServer(cfg, e)
			}

			if err != nil {
				t.Error("dialer", err)
				return
			}
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)
			if release {
				if err := Release(s); err != nil {
					t.Error("Release:", err)
					return
				}
			}
		}
	}
	wg.Add(2)
	go store(false)
	go store(true)
	wg.Wait()

	if n := len(inflightDials); n != 0 {
		t.Errorf("len(inflightDials) == %v, want 0", n)
	}
}

// Some dummy interfaces.
type dummyKey struct {
	testfixtures.DummyKey
	endpoint    upspin.Endpoint
	dialed      int
	pingCount   int
	closeCalled int
}
type dummyStoreServer struct {
	testfixtures.DummyStoreServer
	endpoint upspin.Endpoint
}

type dummyDirServer struct {
	testfixtures.DummyDirServer
	endpoint upspin.Endpoint
}

func (d *dummyKey) Ping() bool {
	d.pingCount++
	return true
}

func (d *dummyKey) Close() {
	d.closeCalled++
}

func (d *dummyKey) Dial(cc upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	user := &dummyKey{endpoint: e}
	d.dialed++
	return user, nil
}

func (d *dummyKey) Endpoint() upspin.Endpoint {
	return d.endpoint
}

func (d *dummyStoreServer) Ping() bool {
	// Add some random delays.
	time.Sleep(time.Duration(rand.Int31n(100)) * time.Millisecond)
	return true
}

func (d *dummyStoreServer) Endpoint() upspin.Endpoint {
	return d.endpoint
}

func (d *dummyStoreServer) Dial(cc upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	// Add some random delays, in order to trigger concurrent dials
	time.Sleep(time.Duration(rand.Int31n(100)) * time.Millisecond)
	store := &dummyStoreServer{endpoint: e}
	return store, nil
}

func (d *dummyDirServer) Dial(cc upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	dir := &dummyDirServer{endpoint: e}
	return dir, nil
}
func (d *dummyDirServer) Endpoint() upspin.Endpoint {
	return d.endpoint
}
func (d *dummyDirServer) Ping() bool {
	// This directory is broken and never reachable.
	return false
}
