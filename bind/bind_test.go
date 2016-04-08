package bind

import (
	"errors"
	"testing"

	"upspin.googlesource.com/upspin.git/upspin"
)

func TestSwitch(t *testing.T) {
	// These should succeed.
	if err := RegisterUser(upspin.InProcess, &dummyUser{}); err != nil {
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
	s1, _ := Store(nil, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr1"})
	s2, _ := Store(nil, upspin.Endpoint{Transport: upspin.InProcess, NetAddr: "addr2"})
	if s1.Endpoint().NetAddr != "addr1" || s2.Endpoint().NetAddr != "addr2" {
		t.Errorf("got %s %s, expected addr1 addr2", s1.Endpoint().NetAddr, s2.Endpoint().NetAddr)
	}

	// This should fail.
	if _, err := Store(nil, upspin.Endpoint{Transport: upspin.Transport(99)}); err == nil {
		t.Errorf("expected bind.Store of undefined to fail")
	}
}

// Some dummy interfaces.
type dummyUser struct {
	endpoint upspin.Endpoint
}
type dummyStore struct {
	endpoint upspin.Endpoint
}
type dummyDirectory struct {
	endpoint upspin.Endpoint
}

func (d *dummyUser) Lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	return nil, nil, errors.New("dummyUser.Lookup not implemented")
}
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	user := &dummyUser{endpoint: e}
	return user, nil
}
func (d *dummyUser) ServerUserName() string {
	return "userUser"
}

func (d *dummyStore) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	return nil, nil, errors.New("dummyStore.Get not implemented")
}
func (d *dummyStore) Put(data []byte) (upspin.Reference, error) {
	return "", errors.New("dummyStore.Put not implemented")
}
func (d *dummyStore) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	store := &dummyStore{endpoint: e}
	return store, nil
}
func (d *dummyStore) Endpoint() upspin.Endpoint {
	return d.endpoint
}
func (d *dummyStore) ServerUserName() string {
	return "userStore"
}
func (d *dummyStore) Delete(ref upspin.Reference) error {
	return errors.New("dummyStore.Delete not implemented")
}

func (d *dummyDirectory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.Lookup not implemented")
}
func (d *dummyDirectory) Put(loc upspin.Location, entry *upspin.DirEntry) error {
	return errors.New("dummyDirectory.Put not implemented")
}
func (d *dummyDirectory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return upspin.Location{}, errors.New("dummyDirectory.MakeDirectory not implemented")
}
func (d *dummyDirectory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.GLob not implemented")
}
func (d *dummyDirectory) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	dir := &dummyDirectory{endpoint: e}
	return dir, nil
}
func (d *dummyDirectory) ServerUserName() string {
	return "userDirectory"
}
func (d *dummyDirectory) Endpoint() upspin.Endpoint {
	return d.endpoint
}
