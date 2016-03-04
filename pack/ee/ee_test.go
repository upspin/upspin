package ee

import (
	"testing"

	"upspin.googlesource.com/upspin.git/key/keyloader"
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

func testPackAndUnpack(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	data := []byte(text)
	meta := upspin.Metadata{}
	cipher := make([]byte, packer.PackLen(ctx, data, &meta, name))
	m, err := packer.Pack(ctx, cipher, data, &meta, name)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	cipher = cipher[:m]

	// Now unpack.
	clear := make([]byte, packer.UnpackLen(ctx, cipher, &meta))
	m, err = packer.Unpack(ctx, clear, cipher, &meta, name)
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
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.256")
		text                    = "this is some text 256"
		packing                 = upspin.EEp256Pack
	)
	ctx, packer := setup(t, user, packing)
	testPackAndUnpack(t, ctx, packer, name, []byte(text))
}

func TestPack521(t *testing.T) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.521")
		text                    = "this is some text 521"
		packing                 = upspin.EEp521Pack
	)
	ctx, packer := setup(t, user, packing)
	testPackAndUnpack(t, ctx, packer, name, []byte(text))
}

func setup(t *testing.T, name upspin.UserName, packing upspin.Packing) (*upspin.Context, upspin.Packer) {
	ctx := &upspin.Context{
		UserName: name,
		Packing:  packing,
	}
	packer := pack.Lookup(packing)
	err := keyloader.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, packer
}
