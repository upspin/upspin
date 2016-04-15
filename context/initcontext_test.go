//Package context creates a client context from various sources.
package context

import (
	"bytes"
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

var once sync.Once

type expectations struct {
	userName  upspin.UserName
	user      upspin.Endpoint
	directory upspin.Endpoint
	store     upspin.Endpoint
	packing   upspin.Packing
}

type envs struct {
	name      string
	user      string
	directory string
	store     string
	packing   string
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

func saveEnvs(e *envs) {
	e.name = os.Getenv("upspinname")
	e.user = os.Getenv("upspinuser")
	e.directory = os.Getenv("upspindirectory")
	e.store = os.Getenv("upspinstore")
	e.packing = os.Getenv("upspinpacking")
}

func restoreEnvs(e *envs) {
	os.Setenv("upspinname", e.name)
	os.Setenv("upspinuser", e.user)
	os.Setenv("upspindirectory", e.directory)
	os.Setenv("upspinstore", e.store)
	os.Setenv("upspinpacking", e.packing)
}

func resetEnvs() {
	var emptyEnv envs
	restoreEnvs(&emptyEnv)
}

func TestMain(m *testing.M) {
	var e envs
	saveEnvs(&e)
	resetEnvs()
	m.Run()
	restoreEnvs(&e)
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

const unimplemented = "unimplemented"

func (d *dummyUser) Lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	panic("unimplemented")
}
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	user := &dummyUser{endpoint: e}
	return user, nil
}
func (d *dummyUser) ServerUserName() string {
	panic("unimplemented")
}

func (d *dummyStore) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	panic("unimplemented")
}
func (d *dummyStore) Put(data []byte) (upspin.Reference, error) {
	panic("unimplemented")
}
func (d *dummyStore) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	store := &dummyStore{endpoint: e}
	return store, nil
}
func (d *dummyStore) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}
func (d *dummyStore) ServerUserName() string {
	panic("unimplemented")
}
func (d *dummyStore) Delete(ref upspin.Reference) error {
	panic("unimplemented")
}

func (d *dummyDirectory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	panic("unimplemented")
}
func (d *dummyDirectory) Put(entry *upspin.DirEntry) (error, upspin.AddWrap) {
	panic("unimplemented")
}
func (d *dummyDirectory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	panic("unimplemented")
}
func (d *dummyDirectory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	panic("unimplemented")
}
func (d *dummyDirectory) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	dir := &dummyDirectory{endpoint: e}
	return dir, nil
}
func (d *dummyDirectory) ServerUserName() string {
	panic("unimplemented")
}
func (d *dummyDirectory) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}
