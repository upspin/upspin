package access_test

import (
	"errors"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"
)

func TestSwitch(t *testing.T) {
	// These should succeed.
	if err := access.Switch.RegisterUser("dummy", &dummyUser{}); err != nil {
		t.Errorf("registerUser failed")
	}
	if err := access.Switch.RegisterStore("dummy", &dummyStore{}); err != nil {
		t.Errorf("registerStore failed")
	}
	if err := access.Switch.RegisterDirectory("dummy", &dummyDirectory{}); err != nil {
		t.Errorf("registerDirectory failed")
	}

	// These should fail.
	if err := access.Switch.RegisterUser("dummy", &dummyUser{}); err == nil {
		t.Errorf("registerUser should have failed")
	}
	if err := access.Switch.RegisterStore("dummy", &dummyStore{}); err == nil {
		t.Errorf("registerStore should have failed")
	}
	if err := access.Switch.RegisterDirectory("dummy", &dummyDirectory{}); err == nil {
		t.Errorf("registerDirectory should have failed")
	}

	// These should return different NetAddrs
	s1, _ := access.Switch.BindStore(nil, upspin.Location{AccessName: "dummy", NetAddr: "addr1"})
	s2, _ := access.Switch.BindStore(nil, upspin.Location{AccessName: "dummy", NetAddr: "addr2"})
	if s1.NetAddr() != "addr1" || s2.NetAddr() != "addr2" {
		t.Errorf("got %s %s, expected addr1 addr2", s1.NetAddr(), s2.NetAddr())
	}

	// This should fail.
	if _, err := access.Switch.BindStore(nil, upspin.Location{AccessName: "undefined"}); err == nil {
		t.Errorf("expected BindStore of undefined to fail")
	}
}

// Some dummy interfaces.
type dummyUser struct {
	loc upspin.Location
}
type dummyStore struct {
	loc upspin.Location
}
type dummyDirectory struct {
	loc upspin.Location
}
type dummyContext int

func (d *dummyContext) Name() string {
	return "george"
}

func (d *dummyUser) Lookup(userName upspin.UserName) ([]upspin.NetAddr, error) {
	return nil, errors.New("dummyUser.Lookup not implemented")
}
func (d *dummyUser) Dial(cc upspin.ClientContext, loc upspin.Location) (interface{}, error) {
	user := &dummyUser{loc: loc}
	return user, nil
}
func (d *dummyUser) ServerUserName() string {
	return "userUser"
}

func (d *dummyStore) Get(location upspin.Location) ([]byte, []upspin.Location, error) {
	return nil, nil, errors.New("dummyStore.Get not implemented")
}
func (d *dummyStore) Put(ref upspin.Reference, data []byte) (upspin.Location, error) {
	return d.loc, errors.New("dummyStore.Put not implemented")
}
func (d *dummyStore) Dial(cc upspin.ClientContext, loc upspin.Location) (interface{}, error) {
	store := &dummyStore{loc: loc}
	return store, nil
}
func (d *dummyStore) NetAddr() upspin.NetAddr {
	return d.loc.NetAddr
}
func (d *dummyStore) ServerUserName() string {
	return "userStore"
}

func (d *dummyDirectory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.Lookup not implemented")
}
func (d *dummyDirectory) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	return upspin.Location{}, errors.New("dummyDirectory.Lookup not implemented")
}
func (d *dummyDirectory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return upspin.Location{}, errors.New("dummyDirectory.MakeDirectory not implemented")
}
func (d *dummyDirectory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.GLob not implemented")
}
func (d *dummyDirectory) Dial(cc upspin.ClientContext, loc upspin.Location) (interface{}, error) {
	dir := &dummyDirectory{loc: loc}
	return dir, nil
}
func (d *dummyDirectory) ServerUserName() string {
	return "userDirectory"
}
