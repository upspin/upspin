//Package context creates a client context from various sources.
package context

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
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

func TestInitContext(t *testing.T) {
	once.Do(func() { registerDummies(t) })
	expect := expectations{
		userName:  "p@google.com",
		user:      upspin.Endpoint{upspin.InProcess, ""},
		directory: upspin.Endpoint{upspin.HTTP, "http://up.your.nose/rubber.hose"},
		store:     upspin.Endpoint{upspin.GCP, "who.knows:1234"},
		packing:   upspin.EEp256Pack,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestDefaults(t *testing.T) {
	once.Do(func() { registerDummies(t) })
	expect := expectations{
		userName:  "noone@nowhere.org",
		user:      upspin.Endpoint{upspin.InProcess, ""},
		directory: upspin.Endpoint{upspin.InProcess, ""},
		store:     upspin.Endpoint{upspin.InProcess, ""},
		packing:   upspin.PlainPack,
	}
	testConfig(t, &expect, "")
}

func TestEnv(t *testing.T) {
	once.Do(func() { registerDummies(t) })
	expect := expectations{
		userName:  "p@google.com",
		user:      upspin.Endpoint{upspin.InProcess, ""},
		directory: upspin.Endpoint{upspin.HTTP, "http://up.your.nose/rubber.hose"},
		store:     upspin.Endpoint{upspin.GCP, "who.knows:1234"},
		packing:   upspin.EEp256Pack,
	}
	config := makeConfig(&expect)
	expect.userName = "quux"
	os.Setenv("upspinname", string(expect.userName))
	expect.directory = upspin.Endpoint{upspin.InProcess, ""}
	os.Setenv("upspindirectory", sprintEndpoint(expect.directory))
	expect.store = upspin.Endpoint{upspin.HTTP, "http://up.your.nose/rubber.hose"}
	os.Setenv("upspinstore", sprintEndpoint(expect.store))
	expect.user = upspin.Endpoint{upspin.GCP, "who.knows:1234"}
	os.Setenv("upspinuser", sprintEndpoint(expect.user))
	expect.packing = upspin.PlainPack
	os.Setenv("upspinpacking", sprintPacking(expect.packing))
	testConfig(t, &expect, config)
}

func makeConfig(expect *expectations) string {
	return fmt.Sprintf("name = %s\nuser= %s\nstore = %s\n  directory =%s   \npacking=%s",
		expect.userName,
		sprintEndpoint(expect.user),
		sprintEndpoint(expect.store),
		sprintEndpoint(expect.directory),
		sprintPacking(expect.packing))
}

func testConfig(t *testing.T, expect *expectations, config string) {
	context := InitContext(bytes.NewBufferString(config))
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
	for _, transport := range []upspin.Transport{upspin.InProcess, upspin.HTTP, upspin.GCP} {
		if err := access.RegisterUser(transport, &dummyUser{}); err != nil {
			t.Errorf("registerUser failed")
		}
		if err := access.RegisterStore(transport, &dummyStore{}); err != nil {
			t.Errorf("registerStore failed")
		}
		if err := access.RegisterDirectory(transport, &dummyDirectory{}); err != nil {
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
	return nil, nil, errors.New("dummyUser.Lookup not implemented")
}
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	user := &dummyUser{endpoint: e}
	return user, nil
}
func (d *dummyUser) ServerUserName() string {
	return "userUser"
}

func (d *dummyStore) Get(key string) ([]byte, []upspin.Location, error) {
	return nil, nil, errors.New("dummyStore.Get not implemented")
}
func (d *dummyStore) Put(data []byte) (string, error) {
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
func (d *dummyStore) Delete(key string) error {
	return errors.New("dummyStore.Delete not implemented")
}

func (d *dummyDirectory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.New("dummyDirectory.Lookup not implemented")
}
func (d *dummyDirectory) Put(name upspin.PathName, data, packdata []byte) (upspin.Location, error) {
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

var transportName = map[upspin.Transport]string{
	upspin.InProcess: "inprocess",
	upspin.GCP:       "gcp",
	upspin.HTTP:      "http",
}

func sprintEndpoint(ep upspin.Endpoint) string {
	return transportName[ep.Transport] + "," + string(ep.NetAddr)
}

var packingName = map[upspin.Packing]string{
	upspin.PlainPack:  "plain",
	upspin.EEp256Pack: "eep256",
	upspin.EEp521Pack: "eep521",
}

func sprintPacking(p upspin.Packing) string {
	return packingName[p]
}
