package usercache

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

type testEntry struct {
	eps []upspin.Endpoint
	pks []upspin.PublicKey
}

type service struct {
	lookups int
	entries map[string]testEntry
}

func setup(t *testing.T) (*service, *upspin.Context) {
	s := &service{entries: make(map[string]testEntry)}
	s.add("a@a.com")
	s.add("b@b.com")
	s.add("c@c.com")
	s.add("d@d.com")

	c := &upspin.Context{
		Packing: upspin.DebugPack,
	}
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	c.User = s

	var err error
	c.Store, err = bind.Store(c, e)
	if err != nil {
		t.Fatal(err)
	}
	c.Directory, err = bind.Directory(c, e)
	if err != nil {
		t.Fatal(err)
	}

	return s, c
}

// TestCache tests the User cache for equivalence with the uncached version and
// for efficacy of the cached version.
func TestCache(t *testing.T) {
	s, c := setup(t)
	Push(c)

	// Cache the 4 names.
	try(t, s, c, "a@a.com")
	try(t, s, c, "b@b.com")
	try(t, s, c, "c@c.com")
	try(t, s, c, "d@d.com")

	sofar := s.lookups

	// Check for consistency between cached and uncached.
	loops := 200
	for i := 0; i < loops; i++ {
		try(t, s, c, "a@a.com")
		try(t, s, c, "b@b.com")
		try(t, s, c, "c@c.com")
		try(t, s, c, "d@d.com")
	}

	// If the cache worked, we should only have 1 uncached access per try() in the loop.
	if s.lookups != sofar+4*loops {
		t.Errorf("uncached loookups, got %d, expected %d", s.lookups, sofar+4*loops)
	}
}

// TestExpiration tests that cache entries time out.
func TestExpiration(t *testing.T) {
	s, c := setup(t)
	Push(c)
	c.User.(*userCache).SetDuration(time.Second)

	// Cache the 4 names.
	try(t, s, c, "a@a.com")
	try(t, s, c, "b@b.com")
	try(t, s, c, "c@c.com")
	try(t, s, c, "d@d.com")
	sofar := s.lookups

	// After a few seconds all entries should expire.
	try(t, s, c, "a@a.com")
	try(t, s, c, "b@b.com")
	try(t, s, c, "c@c.com")
	try(t, s, c, "d@d.com")
	if s.lookups != sofar+4 {
		t.Errorf("uncached loookups, got %d, expected %d", s.lookups, sofar+4)
	}
}

// try looks up a name through the cached and uncached User services and
// compares the results.
func try(t *testing.T, s *service, c *upspin.Context, name string) {
	seps, spks, serr := s.Lookup(upspin.UserName(name))
	ceps, cpks, cerr := c.User.Lookup(upspin.UserName(name))
	if !reflect.DeepEqual(seps, ceps) {
		t.Errorf("for %s got %v expect %v", name, ceps, seps)
	}
	if !reflect.DeepEqual(spks, cpks) {
		t.Errorf("for %s got %v expect %v", name, cpks, spks)
	}
	if !reflect.DeepEqual(serr, cerr) {
		t.Errorf("for %s got %v expect %v", name, cerr, serr)
	}
}

// service is a User implementation that counts lookups.
func (s *service) add(name string) {
	var e testEntry
	for i := 0; i < 3; i++ {
		ep := upspin.Endpoint{
			Transport: upspin.InProcess,
			NetAddr:   upspin.NetAddr(fmt.Sprintf("%s%d", name, i)),
		}
		e.eps = append(e.eps, ep)
	}
	for i := 0; i < 3; i++ {
		pk := upspin.PublicKey(fmt.Sprintf("%s%d", name, i))
		e.pks = append(e.pks, pk)
	}
	s.entries[name] = e
}

func (s *service) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	s.lookups++
	if e, ok := s.entries[string(name)]; ok {
		return e.eps, e.pks, nil
	}
	return nil, nil, errors.New("not found")
}

func (s *service) ServerUserName() string {
	return "?"
}

func (s *service) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	return s, nil
}
