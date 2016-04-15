package plain

import (
	"testing"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	context = &upspin.Context{}
	meta    = &upspin.Metadata{
		Packdata: []byte{byte(upspin.PlainPack)},
	}
)

func TestRegister(t *testing.T) {
	p := pack.Lookup(upspin.PlainPack)
	if p == nil {
		t.Fatal("Lookup failed")
	}
	if p.Packing() != upspin.PlainPack {
		t.Fatalf("expected %q got %q", plainPack{}, p)
	}
}

func TestPack(t *testing.T) {
	const (
		name upspin.PathName = "user@google.com/file/of/user"
		text                 = "this is some text"
	)
	packer := plainPack{}

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
