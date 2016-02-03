package testpack

import (
	"testing"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.Debug)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.Debug {
		t.Fatalf("expected %q got %q", testPack{}, p)
	}
}

// The values returned by PackLen and UnpackLen should be exact,
// but that is not a requirement for the Packer interface in general.
// We test the precision here though.
func TestPackLen(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user"
		text                 = "this is some text"
	)
	packer := testPack{}

	// First pack.
	data := []byte(text)
	n := packer.PackLen(data, nil, name)
	if n < 0 {
		t.Fatal("PackLen failed")
	}
	cipher := make([]byte, n)
	m, err := packer.Pack(cipher, data, nil, name)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	if n != m {
		t.Fatalf("Pack returned %d; PackLen returned %d", m, n)
	}
	cipher = cipher[:m] // Already true, but be thorough.

	// Now unpack.
	n = packer.UnpackLen(cipher, nil)
	if n < 0 {
		t.Fatal("UnpackLen failed")
	}
	clear := make([]byte, n)
	clearName, m, err := packer.Unpack(clear, cipher, nil)
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
	if clearName != name {
		t.Errorf("name: expected %q; got %q", name, clearName)
	}
}

// This one uses oversize buffers rather than what PackLen says.
// Verifies that the count returned is correct if the buffer is longer than needed.
func TestPack(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user"
		text                 = "this is some text"
	)
	packer := testPack{}

	// First pack.
	data := []byte(text)
	cipher := make([]byte, 1024)
	m, err := packer.Pack(cipher, data, nil, name)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	cipher = cipher[:m]

	// Now unpack.
	clear := make([]byte, 1024)
	clearName, m, err := packer.Unpack(clear, cipher, nil)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	clear = clear[:m]
	str := string(clear[:m])
	if str != text {
		t.Errorf("text: expected %q; got %q", text, str)
	}
	if clearName != name {
		t.Errorf("name: expected %q; got %q", name, clearName)
	}
}
