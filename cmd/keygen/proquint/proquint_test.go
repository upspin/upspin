package proquint

import "testing"

func TestEncode(t *testing.T) {
	cases := []struct {
		x uint
		s string
	}{
		{0x7f000001, "lusab-babad"},
		{0x3f54dcc1, "gutih-tugad"},
		{0x3f760723, "gutuk-bisog"},
		{0x8c62c18d, "mudof-sakat"},
	}
	for _, c := range cases {
		s := string(Encode(uint16(c.x >> 16)))
		if s != c.s[:5] {
			t.Errorf("Encode(%x) == %q, want %q", c.x, s, c.s)
		}
		s = string(Encode(uint16(c.x & 0xffff)))
		if s != c.s[6:] {
			t.Errorf("Encode(%x) == %q, want %q", c.x, s, c.s)
		}
		x := Decode([]byte(c.s[:5]))
		if x != uint16(c.x>>16) {
			t.Errorf("Decode(%q) == %x, want %x", c.s[:5], x, uint16(c.x>>16))
		}
		x = Decode([]byte(c.s[6:]))
		if x != uint16(c.x&0xffff) {
			t.Errorf("Decode(%q) == %x, want %x", c.s[6:], x, uint16(c.x&0xffff))
		}
	}
	x := Decode([]byte("xxxxx"))
	if x == 0 {
		t.Errorf("Decode(\"xxxxx\") = %x", x)
	}
}
