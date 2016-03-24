//Package context creates a client context from various sources.
package context

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/pack"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	"upspin.googlesource.com/upspin.git/upspin"
)

var errUnimplemented = errors.New("unimplemented")

var once sync.Once

type expectations struct {
	userName  upspin.UserName
	user      upspin.Endpoint
	directory upspin.Endpoint
	store     upspin.Endpoint
	packing   upspin.Packing
}

// Endpoint is a helper to make it easier to build vet-error-free upspin.Endpoints.
func Endpoint(t upspin.Transport, n upspin.NetAddr) upspin.Endpoint {
	return upspin.Endpoint{
		Transport: t,
		NetAddr:   n,
	}
}

func TestInitContext(t *testing.T) {
	once.Do(func() { registerDummies(t) })
	expect := expectations{
		userName:  "p@google.com",
		user:      Endpoint(upspin.InProcess, ""),
		directory: Endpoint(upspin.GCP, "who.knows:1234"),
		store:     Endpoint(upspin.GCP, "who.knows:1234"),
		packing:   upspin.EEp256Pack,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestDefaults(t *testing.T) {
	once.Do(func() { registerDummies(t) })
	expect := expectations{
		userName:  "noone@nowhere.org",
		user:      Endpoint(upspin.InProcess, ""),
		directory: Endpoint(upspin.InProcess, ""),
		store:     Endpoint(upspin.InProcess, ""),
		packing:   upspin.PlainPack,
	}
	testConfig(t, &expect, "")
}

func TestEnv(t *testing.T) {
	once.Do(func() { registerDummies(t) })
	expect := expectations{
		userName:  "p@google.com",
		user:      Endpoint(upspin.InProcess, ""),
		directory: Endpoint(upspin.GCP, "who.knows:1234"),
		store:     Endpoint(upspin.GCP, "who.knows:1234"),
		packing:   upspin.EEp256Pack,
	}
	config := makeConfig(&expect)
	expect.userName = "quux"
	os.Setenv("upspinname", string(expect.userName))
	expect.directory = Endpoint(upspin.InProcess, "")
	os.Setenv("upspindirectory", endpoint.String(&expect.directory))
	expect.store = Endpoint(upspin.GCP, "who.knows:1234")
	os.Setenv("upspinstore", endpoint.String(&expect.store))
	expect.user = Endpoint(upspin.GCP, "who.knows:1234")
	os.Setenv("upspinuser", endpoint.String(&expect.user))
	expect.packing = upspin.PlainPack
	os.Setenv("upspinpacking", pack.Lookup(expect.packing).String())
	testConfig(t, &expect, config)
}

func makeConfig(expect *expectations) string {
	return fmt.Sprintf("name = %s\nuser= %s\nstore = %s\n  directory =%s   \npacking=%s",
		expect.userName,
		endpoint.String(&expect.user),
		endpoint.String(&expect.store),
		endpoint.String(&expect.directory),
		pack.Lookup(expect.packing).String())
}

func testConfig(t *testing.T, expect *expectations, config string) {
	context, err := InitContext(bytes.NewBufferString(config))
	if err != nil {
		t.Fatalf("could not parse config %s: %s", config, err)
	}
	if context.UserName != expect.userName {
		t.Errorf("name: got %s expected %s", context.UserName, expect.userName)
	}
	tests := []struct {
		expected upspin.Endpoint
		got      upspin.Endpoint
	}{
		{expect.user, context.User.(*dummyUser).endpoint},
		{expect.directory, context.Directory.(*dummyDirectory).endpoint},
		{expect.store, context.Store.(*dummyStore).endpoint},
	}
	for _, test := range tests {
		if test.expected != test.got {
			t.Errorf("got %v expected %v", test.got, test.expected)
		}
	}
	if context.Packing != expect.packing {
		t.Errorf("got %v expected %v", context.Packing, expect.packing)
	}
}

func registerDummies(t *testing.T) {
	for _, transport := range []upspin.Transport{upspin.InProcess, upspin.GCP} {
		if err := bind.RegisterUser(transport, &dummyUser{}); err != nil {
			t.Errorf("registerUser failed")
		}
		if err := bind.RegisterStore(transport, &dummyStore{}); err != nil {
			t.Errorf("registerStore failed")
		}
		if err := bind.RegisterDirectory(transport, &dummyDirectory{}); err != nil {
			t.Errorf("registerDirectory failed")
		}
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
	return nil, nil, errUnimplemented
}
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	user := &dummyUser{endpoint: e}
	return user, nil
}
func (d *dummyUser) ServerUserName() string {
	return "userUser"
}

func (d *dummyStore) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	return nil, nil, errUnimplemented
}
func (d *dummyStore) Put(data []byte) (upspin.Reference, error) {
	return "", errUnimplemented
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
	return errUnimplemented
}

func (d *dummyDirectory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.Lookup not implemented")
}
func (d *dummyDirectory) Put(name upspin.PathName, data []byte, packdata upspin.PackData, opts *upspin.PutOptions) (upspin.Location, error) {
	return upspin.Location{}, errors.New("dummyDirectory.Lookup not implemented")
}
func (d *dummyDirectory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return upspin.Location{}, errors.New("dummyDirectory.MakeDirectory not implemented")
}
func (d *dummyDirectory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.Glob not implemented")
}
func (d *dummyDirectory) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	dir := &dummyDirectory{endpoint: e}
	return dir, nil
}
func (d *dummyDirectory) ServerUserName() string {
	return "userDirectory"
}
