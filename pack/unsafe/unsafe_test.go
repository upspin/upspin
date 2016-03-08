package unsafe

import (
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"
)

var (
	user    = upspin.UserName("me@me.com")
	context = &upspin.Context{
		UserName: user,
		Packing:  upspin.UnsafePack,
	}
	testUser *testuser.Service
)

func setup() UnsafePack {
	packer := pack.Lookup(upspin.UnsafePack)
	u, ok := packer.(UnsafePack)
	if !ok {
		panic("not unsafe pack")
	}
	endpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	var err error
	context.User, err = bind.User(context, endpoint)
	if err != nil {
		panic(err)
	}
	testUser = context.User.(*testuser.Service)
	// Add an initial public key for the current user
	key := makeUserKey(user, []byte("NaCl"))
	testUser.SetPublicKeys(user, []upspin.PublicKey{key})
	context.KeyPair = upspin.KeyPair{
		Public:  key,
		Private: upspin.PrivateKey(string(key)),
	}
	return u
}

func makeUserKey(userName upspin.UserName, salt []byte) upspin.PublicKey {
	return upspin.PublicKey(string(xor([]byte(userName), salt)))
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
	if len(bytes) != 164 {
		t.Fatalf("Expected 164 bytes, got %d", len(bytes))
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

	clear := []byte("this is data")
	path := upspin.PathName("me@me.com/folder/dir/file.txt")
	meta := upspin.Metadata{}
	packLen := u.PackLen(context, clear, &meta, path)
	cipher := make([]byte, packLen+5)
	n, err := u.Pack(context, cipher, clear, &meta, path)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if n > packLen {
		t.Errorf("Expected %d bytes, got %d", packLen, n)
	}
	cipher = cipher[:n]
	if len(meta.PackData) == 0 {
		t.Error("PackData length is zero")
	}

	unpackLen := u.PackLen(context, cipher, &meta, path)
	unpacked := make([]byte, unpackLen+10)
	n, err = u.Unpack(context, unpacked, cipher, &meta, path)
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

	clear := []byte("this is data")
	path := upspin.PathName("me@me.com/folder/dir/file.txt")
	meta := upspin.Metadata{}
	packLen := u.PackLen(context, clear, &meta, path)
	cipher := make([]byte, packLen)
	n, err := u.Pack(context, cipher, clear, &meta, path)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	log.Printf("cipher1: %v", string(cipher))

	newUser := upspin.UserName("someone@them.com")
	newUserKey := makeUserKey(newUser, []byte("random stuff"))
	testUser.SetPublicKeys(newUser, []upspin.PublicKey{newUserKey})

	// Pack again.
	n, err = u.Pack(context, cipher, clear, &meta, path)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	log.Printf("cipher2: %v", string(cipher))

	// Simulates the data being looked up by a new user, with whom
	// access has been shared. Check that it works.
	context.UserName = newUser
	context.KeyPair = upspin.KeyPair{
		Public:  upspin.PublicKey(newUserKey),
		Private: upspin.PrivateKey(newUserKey),
	}
	unpackLen := u.UnpackLen(context, cipher, &meta)
	unpacked := make([]byte, unpackLen)
	n, err = u.Unpack(context, unpacked, cipher, &meta, path)
	if err != nil {
		t.Error(err)
	}
	unpacked = unpacked[:n]

	if string(unpacked) != string(clear) {
		t.Errorf("Expected unpacked %q, got %q", string(clear), string(unpacked))
	}

	// Now, to double-check, pretend the original writer's key
	// changed. The signature will no longer be valid.
	testUser.SetPublicKeys(user, []upspin.PublicKey{makeUserKey(user, []byte("crazy bits"))})
	n, err = u.Unpack(context, unpacked, cipher, &meta, path)
	if err == nil {
		t.Fatalf("Expected error, got none")
	}
	expectedErrorPrefix := "expected signature"
	if !strings.HasPrefix(err.Error(), expectedErrorPrefix) {
		t.Errorf("Expected error prefix %q, got error %q", expectedErrorPrefix, err)
	}
}
