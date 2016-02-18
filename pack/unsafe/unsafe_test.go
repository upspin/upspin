package unsafe

import (
	"log"
	"testing"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

func setup() *UnsafePack {
	packer := pack.Lookup(upspin.UnsafePack)
	u, ok := packer.(*UnsafePack)
	if !ok {
		panic("not unsafe pack")
	}
	return u
}

func TestPackMeta(t *testing.T) {
	u := setup()

	meta := clearMeta{
		WrappedKeys: []wrappedKey{
			wrappedKey{
				User:    upspin.UserName("bob@bob.com"),
				Wrapped: []byte("bob's wrapped key"),
			},
			wrappedKey{
				User:    upspin.UserName("larry@larry.com"),
				Wrapped: []byte("larry's wrapped key"),
			},
		},
		Signature: signature(91873),
	}
	bytes := u.packMeta(&meta)
	if len(bytes) != 163 {
		t.Fatalf("Expected 163 bytes, got %d", len(bytes))
	}
	unpackedMeta := u.unpackMeta(bytes)
	if len(unpackedMeta.WrappedKeys) != 2 {
		t.Fatalf("Expected 2 wrapped keys, got %d", len(unpackedMeta.WrappedKeys))
	}
	if unpackedMeta.Signature != meta.Signature {
		t.Fatalf("Expected signature %v, got %v", meta.Signature, unpackedMeta.Signature)
	}
}

func TestPackUnpack(t *testing.T) {
	u := setup()

	user := upspin.UserName("me@me.com")
	u.SetCurrentUser(user)
	u.AddUserKeys(user, u.MakeUserKeys(user))

	clear := []byte("this is data")
	path := upspin.PathName("me@me.com/folder/dir/file.txt")
	meta := upspin.Metadata{}
	packLen := u.PackLen(clear, &meta, path)
	cipher := make([]byte, packLen+5)
	n, err := u.Pack(cipher, clear, &meta, path)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n > packLen {
		t.Errorf("Expected %d bytes, got %d", packLen, n)
	}
	cipher = cipher[:n]

	unpackLen := u.PackLen(cipher, &meta, path)
	unpacked := make([]byte, unpackLen+10)
	n, err = u.Unpack(unpacked, cipher, &meta, path)
	if err != nil {
		t.Error(err)
	}
	unpacked = unpacked[:n]

	if string(unpacked) != string(clear) {
		t.Errorf("Expected unpacked %q, got %q", string(clear), string(unpacked))
	}

	log.Printf("meta: %v+", meta)
}

func TestSharing(t *testing.T) {
	u := setup()

	user := upspin.UserName("me@me.com")
	u.SetCurrentUser(user)
	u.AddUserKeys(user, u.MakeUserKeys(user))

	clear := []byte("this is data")
	path := upspin.PathName("me@me.com/folder/dir/file.txt")
	meta := upspin.Metadata{}
	packLen := u.PackLen(clear, &meta, path)
	cipher := make([]byte, packLen)
	n, err := u.Pack(cipher, clear, &meta, path)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	log.Printf("cipher1: %v", string(cipher))

	newUser := upspin.UserName("someone@them.com")
	u.AddUserKeys(newUser, u.MakeUserKeys(newUser))

	// Pack again.
	n, err = u.Pack(cipher, clear, &meta, path)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	log.Printf("cipher2: %v", string(cipher))

	// Simulates the data being looked up by a new user, with whom
	// access has been shared. Check that it works.
	u.SetCurrentUser(newUser)
	u.AddUserKeys(user, u.MakeUserKeys(user)) // Garble the owner's key to ensure we're not using them
	unpackLen := u.UnpackLen(cipher, &meta)
	unpacked := make([]byte, unpackLen)
	n, err = u.Unpack(unpacked, cipher, &meta, path)
	if err != nil {
		t.Error(err)
	}
	unpacked = unpacked[:n]

	if string(unpacked) != string(clear) {
		t.Errorf("Expected unpacked %q, got %q", string(clear), string(unpacked))
	}
}
