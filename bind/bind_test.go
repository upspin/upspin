package bind

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"upspin.io/test/testfixtures"
	"upspin.io/upspin"
)

func TestSwitch(t *testing.T) {
	var ctx upspin.Context

	// These should succeed.
	du := &dummyUser{}
	if err := RegisterUser(upspin.InProcess, du); err != nil {
		t.Errorf("registerUser failed")
	}
	if err := RegisterStore(upspin.InProcess, &dummyStore{}); err != nil {
		t.Errorf("registerStore failed")
	}
	if err := RegisterDirectory(upspin.InProcess, &dummyDirectory{}); err != nil {
		t.Errorf("registerDirectory failed")
	}

	// These should fail.
	if err := RegisterUser(upspin.InProcess, &dummyUser{}); err == nil {
		t.Errorf("registerUser should have failed")
	}
	if err := RegisterStore(upspin.InProcess, &dummyStore{}); err == nil {
		t.Errorf("registerStore should have failed")
	}
	if err := RegisterDirectory(upspin.InProcess, &dummyDirectory{}); err == nil {
		t.Errorf("registerDirectory should have failed")
	}

	// These should return different NetAddrs
	s1, _ := Store(&ctx, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr1"})
	s2, _ := Store(&ctx, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr2"})
	if s1.Endpoint().NetAddr != "addr1" || s2.Endpoint().NetAddr != "addr2" {
		t.Errorf("got %s %s, expected addr1 addr2", s1.Endpoint().NetAddr, s2.Endpoint().NetAddr)
	}

	// This should fail.
	if _, err := Store(&ctx, upspin.Endpoint{Transport: upspin.Transport(99)}); err == nil {
		t.Errorf("expected bind.Store of undefined to fail")
	}

	// Directory is never reachable (our dummyDirectory answers false to ping)
	_, err := Directory(&ctx, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr1"})
	if err == nil {
		t.Error("Expected error")
	}
	const expectedError = "Ping failed"
	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected %q error, got %q", expectedError, err)
	}

	// Test caching. dummyUser has a dial count.
	e := upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr1"}
	u1, err := User(&ctx, e) // Dials once.
	if err != nil {
		t.Fatal(err)
	}
	u2, err := User(&ctx, e) // Does not dial; hits the cache.
	if err != nil {
		t.Fatal(err)
	}
	if u1 != u2 {
		t.Errorf("Expected the same instance.")
	}
	if du.dialed != 1 {
		t.Errorf("Expected only one dial. Got %d", du.dialed)
	}
	// But a different context forces a new dial.
	ctx2 := upspin.Context{
		UserName: upspin.UserName("bob@foo.com"),
	}
	u3, err := User(&ctx2, e) // Dials again,
	if err != nil {
		t.Fatal(err)
	}
	if du.dialed != 2 {
		t.Errorf("Expected two dials. Got %d", du.dialed)
	}
	if u1.(*dummyUser).pingCount != 1 {
		t.Errorf("Expected only one ping. Got %d", du.pingCount)
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

	if u1.(*dummyUser).closeCalled != 1 {
		t.Errorf("Expected close to be called once on u1")
	}
	if u3.(*dummyUser).closeCalled != 1 {
		t.Errorf("Expected close to be called once on u3")
	}
}

func TestConcurrency(t *testing.T) {
	const nRuns = 10
	pingFreshnessDuration = 0 // Forces ping to always be invalid
	defer func() { pingFreshnessDuration = 15 * time.Minute }()

	var ctx upspin.Context
	e := upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr17"}

	var wg sync.WaitGroup
	store := func(release bool) {
		defer wg.Done()
		for i := 0; i < nRuns; i++ {
			s, err := Store(&ctx, e)
			if err != nil {
				t.Error("Store:", err)
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
type dummyUser struct {
	testfixtures.DummyUser
	endpoint    upspin.Endpoint
	dialed      int
	pingCount   int
	closeCalled int
}
type dummyStore struct {
	testfixtures.DummyStore
	endpoint upspin.Endpoint
}
type dummyDirectory struct {
	testfixtures.DummyDirectory
	endpoint upspin.Endpoint
}

func (d *dummyUser) Ping() bool {
	d.pingCount++
	return true
}
func (d *dummyUser) Close() {
	d.closeCalled++
}

func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	user := &dummyUser{endpoint: e}
	d.dialed++
	return user, nil
}

func (d *dummyUser) Endpoint() upspin.Endpoint {
	return d.endpoint
}

func (d *dummyStore) Ping() bool {
	// Add some random delays.
	time.Sleep(time.Duration(rand.Int31n(100)) * time.Millisecond)
	return true
}

func (d *dummyStore) Endpoint() upspin.Endpoint {
	return d.endpoint
}

func (d *dummyStore) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	store := &dummyStore{endpoint: e}
	return store, nil
}

func (d *dummyDirectory) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	dir := &dummyDirectory{endpoint: e}
	return dir, nil
}
func (d *dummyDirectory) Endpoint() upspin.Endpoint {
	return d.endpoint
}
func (d *dummyDirectory) Ping() bool {
	// This directory is broken and never reachable.
	return false
}
