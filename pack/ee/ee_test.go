package ee

import (
	"testing"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.EEp256Pack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.EEp256Pack {
		t.Fatalf("expected EEp256Pack, got %q", p)
	}
}

func testPackAndUnpack(t *testing.T, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	data := []byte(text)
	meta := upspin.Metadata{}
	cipher := make([]byte, packer.PackLen(nil, data, &meta, name))
	m, err := packer.Pack(nil, cipher, data, &meta, name)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	cipher = cipher[:m]

	// Now unpack.
	clear := make([]byte, packer.UnpackLen(nil, cipher, &meta))
	m, err = packer.Unpack(nil, clear, cipher, &meta, name)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	clear = clear[:m]
	str := string(clear[:m])
	if str != string(text) {
		t.Errorf("text: expected %q; got %q", text, str)
	}
}

func TestPack256(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user.256"
		text                 = "this is some text 256"
	)
	packer := pack.Lookup(upspin.EEp256Pack)
	testPackAndUnpack(t, packer, name, []byte(text))
}

func TestPack521(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user.521"
		text                 = "this is some text 521"
	)
	packer := pack.Lookup(upspin.EEp521Pack)
	testPackAndUnpack(t, packer, name, []byte(text))
}
