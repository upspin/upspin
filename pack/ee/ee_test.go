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

func TestPack(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user"
		text                 = "this is some text"
	)
	packer := pack.Lookup(upspin.EEp256Pack)

	// First pack.
	data := []byte(text)
	cipher := make([]byte, 1024)
	meta := upspin.Metadata{}
	m, err := packer.Pack(nil, cipher, data, &meta, name)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	cipher = cipher[:m]

	// Now unpack.
	clear := make([]byte, 1024)
	m, err = packer.Unpack(nil, clear, cipher, &meta, name)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	clear = clear[:m]
	str := string(clear[:m])
	if str != text {
		t.Errorf("text: expected %q; got %q", text, str)
	}
}
