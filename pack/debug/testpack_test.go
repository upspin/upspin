package debugpack

import (
	"testing"

	"upspin.io/pack"
	"upspin.io/upspin"
)

var (
	meta = &upspin.Metadata{
		Packdata: []byte{byte(upspin.DebugPack)},
	}
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.DebugPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.DebugPack {
		t.Fatalf("expected %q got %q", testPack{}, p)
	}
}

const (
	name     upspin.PathName = "user@google.com/file/of/user"
	text                     = "this is some text"
	userName                 = "joe@blow.com"
)

var (
	context = &upspin.Context{
		User:     &dummyUser{},
		UserName: userName,
	}
)

// The values returned by PackLen and UnpackLen should be exact,
// but that is not a requirement for the Packer interface in general.
// We test the precision here though.
func TestPackLen(t *testing.T) {
	packer := testPack{}

	// First pack.
	data := []byte(text)
	de := &upspin.DirEntry{
		Name: name,
	}
	n := packer.PackLen(context, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	cipher := make([]byte, n)
	m, err := packer.Pack(context, cipher, data, de)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	if n != m {
		t.Fatalf("Pack returned %d; PackLen returned %d", m, n)
	}
	cipher = cipher[:m] // Already true, but be thorough.

	// Now unpack.
	n = packer.UnpackLen(context, cipher, de)
	if n < 0 {
		t.Fatal("UnpackLen failed")
	}
	clear := make([]byte, n)
	m, err = packer.Unpack(context, clear, cipher, de)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	if n != m {
		t.Fatalf("Unpack returned %d; UnpackLen returned %d", m, n)
	}
	clear = clear[:m] // Already true, but be thorough.
	str := string(clear[:m])
	if str != text {
		t.Errorf("text: expected %q; got %q", text, str)
	}
}

// This one uses oversize buffers rather than what PackLen says.
// Verifies that the count returned is correct if the buffer is longer than needed.
func TestPack(t *testing.T) {
	packer := testPack{}

	// First pack.
	data := []byte(text)
	cipher := make([]byte, 1024)
	de := &upspin.DirEntry{
		Name: name,
	}
	n := packer.PackLen(context, data, de)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	m, err := packer.Pack(context, cipher, data, de)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	cipher = cipher[:m]

	// Now unpack.
	clear := make([]byte, 1024)
	m, err = packer.Unpack(context, clear, cipher, de)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	clear = clear[:m]
	str := string(clear[:m])
	if str != text {
		t.Errorf("text: expected %q; got %q", text, str)
	}
}

// Dummy interface for User, so we can look up a user and get a key.

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
	return nil, []upspin.PublicKey{"a key"}, nil
}

func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return d, nil
}

func (d *dummyUser) ServerUserName() string {
	return "dummyUser"
}

func (d *dummyUser) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}

func (d *dummyUser) Configure(options ...string) error {
	panic("unimplemented")
}

func (d *dummyUser) Ping() bool {
	return true
}
