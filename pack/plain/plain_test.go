package plain

import (
	"testing"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
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
