package sha256key

import "testing"

func TestKeyParse(t *testing.T) {
	var key Hash
	for i := range key {
		k := byte(i & 0x0F)
		key[i] = 0x10*k + k // So we get 00112233 etc.
	}
	str := key.String()
	nkey, err := Parse(str)
	if err != nil {
		t.Fatal(err)
	}
	if nkey != key {
		t.Fatalf("want %s got %s", key, nkey)
	}
	// Now an error
	str = str[1:]
	_, err = Parse(str)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestAllocsForOf(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping malloc count in short mode")
	}
	data := make([]byte, 1024)
	hash := func() {
		Of(data)
	}
	allocs := testing.AllocsPerRun(1000, hash)
	if allocs != 0 {
		t.Fatalf("allocs per hash: %g should be zero", allocs)
	}
}

func TestOf(t *testing.T) {
	// This is a golden test, just to make sure that the Of function works.
	// The function is trivial but it's worth making sure its output doesn't break.
	key := Of([]byte("hello, world"))
	const want = "09CA7E4EAA6E8AE9C7D261167129184883644D07DFBA7CBFBC4C8A2E08360D5B"
	if !key.EqualString(want) {
		t.Fatal("incorrect key")
	}
}
