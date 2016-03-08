package ee

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"

	// let's generate keys internally rather than depend on tester's files   "upspin.googlesource.com/upspin.git/key/keyloader"
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

// packBlob packs text according to the parameters and returns the cipher.
func packBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name upspin.PathName, meta *upspin.Metadata, text []byte) []byte {
	// TODO why data?
	data := []byte(text)
	cipher := make([]byte, packer.PackLen(ctx, data, meta, name))
	m, err := packer.Pack(ctx, cipher, data, meta, name)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	return cipher[:m]
}

// unpackBlob unpacks cipher according to the parameters and returns the plain text.
func unpackBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name upspin.PathName, meta *upspin.Metadata, cipher []byte) []byte {
	clear := make([]byte, packer.UnpackLen(ctx, cipher, meta))
	m, err := packer.Unpack(ctx, clear, cipher, meta, name)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	return clear[:m]
}

func testPackAndUnpack(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	meta := &upspin.Metadata{}
	cipher := packBlob(t, ctx, packer, name, meta, text)

	// Now unpack.
	clear := unpackBlob(t, ctx, packer, name, meta, cipher)

	// TODO why str?
	str := string(clear)
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
	ctx, packer := setup(user, packing)
	testPackAndUnpack(t, ctx, packer, name, []byte(text))
}

func TestPack521(t *testing.T) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.521")
		text                    = "this is some text 521"
		packing                 = upspin.EEp521Pack
	)
	ctx, packer := setup(user, packing)
	testPackAndUnpack(t, ctx, packer, name, []byte(text))
}

func BenchmarkPack256(b *testing.B) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.256")
		text                    = "this is some text 256"
		packing                 = upspin.EEp256Pack
	)
	ctx, packer := setup(user, packing)
	for i := 0; i < b.N; i++ {
		meta := &upspin.Metadata{}
		data := []byte(text)
		cipher := make([]byte, packer.PackLen(ctx, data, meta, name))
		m, _ := packer.Pack(ctx, cipher, data, meta, name)
		cipher = cipher[:m]
		clear := make([]byte, packer.UnpackLen(ctx, cipher, meta))
		m, _ = packer.Unpack(ctx, clear, cipher, meta, name)
		clear = clear[:m]
	}
}

func BenchmarkPack384(b *testing.B) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.256")
		text                    = "this is some text 384"
		packing                 = upspin.EEp384Pack
	)
	ctx, packer := setup(user, packing)
	for i := 0; i < b.N; i++ {
		meta := &upspin.Metadata{}
		data := []byte(text)
		cipher := make([]byte, packer.PackLen(ctx, data, meta, name))
		m, _ := packer.Pack(ctx, cipher, data, meta, name)
		cipher = cipher[:m]
		clear := make([]byte, packer.UnpackLen(ctx, cipher, meta))
		m, _ = packer.Unpack(ctx, clear, cipher, meta, name)
		clear = clear[:m]
	}
}

func BenchmarkPack521(b *testing.B) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.256")
		text                    = "this is some text 521"
		packing                 = upspin.EEp521Pack
	)
	ctx, packer := setup(user, packing)
	for i := 0; i < b.N; i++ {
		meta := &upspin.Metadata{}
		data := []byte(text)
		cipher := make([]byte, packer.PackLen(ctx, data, meta, name))
		m, _ := packer.Pack(ctx, cipher, data, meta, name)
		cipher = cipher[:m]
		clear := make([]byte, packer.UnpackLen(ctx, cipher, meta))
		m, _ = packer.Unpack(ctx, clear, cipher, meta, name)
		clear = clear[:m]
	}
}

func TestLoadingRemoteKeys(t *testing.T) {
	// dude@google.com is the owner of a file that is shared with bob@foo.com.
	const (
		dudesUserName upspin.UserName = "dude@google.com"
		packing                       = upspin.EEp256Pack
		pathName                      = upspin.PathName(dudesUserName + "/secret_file_shared_with_bob")
		bobsUserName  upspin.UserName = "bob@foo.com"
		text                          = "bob, here's the secret file. Sincerely, The Dude."
	)
	dudesPrivKey := upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
	bobsPrivKey := upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528"),
		Private: upspin.PrivateKey("93177533964096447201034856864549483929260757048490326880916443359483929789924"),
	}

	// Set up Dude as the creator/owner.
	ctx, packer := setup(dudesUserName, packing)
	// Set up a mock user service that knows about Bob's and Dude's public keys.
	mockUser := &dummyUser{
		userToMatch: []upspin.UserName{bobsUserName, dudesUserName},
		keyToReturn: []upspin.PublicKey{bobsPrivKey.Public, dudesPrivKey.Public},
	}
	ctx.KeyPair = dudesPrivKey // Override setup to prevent reading keys from .ssh/
	ctx.User = mockUser

	// Setup the metadata such that Bob is a reader.
	meta := &upspin.Metadata{
		Readers: []upspin.UserName{bobsUserName},
	}
	cipher := packBlob(t, ctx, packer, pathName, meta, []byte(text))

	// Interim check: dummyUser returned Bob's public key when asked.
	if mockUser.returnedKeys != 1 {
		t.Fatal("Packer failed to request Bob's public key")
	}

	// Now load Bob as the current user.
	ctx.UserName = bobsUserName
	ctx.KeyPair = bobsPrivKey

	clear := unpackBlob(t, ctx, packer, pathName, meta, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}

	// Finally, check that unpack looked up Dude's public key, to verify the signature.
	if mockUser.returnedKeys != 2 {
		t.Fatal("Packer failed to request dude's public key")
	}
}

func setup(name upspin.UserName, packing upspin.Packing) (*upspin.Context, upspin.Packer) {
	// because ee.common.curve is not exported
	curve := []elliptic.Curve{16: elliptic.P256(), 18: elliptic.P384(), 17: elliptic.P521()}

	ctx := &upspin.Context{
		UserName: name,
		Packing:  packing,
	}
	packer := pack.Lookup(packing)
	priv, err := ecdsa.GenerateKey(curve[packing], rand.Reader)
	if err != nil {
		// would be nice to t.Fatal but then can't call from Benchmark?
		return ctx, packer
	}
	ctx.KeyPair = upspin.KeyPair{
		upspin.PublicKey(fmt.Sprintf("%s\n%s\n%s\n", packer.String(), priv.X.String(), priv.Y.String())),
		upspin.PrivateKey(fmt.Sprintf("%s\n", priv.D.String())),
	}
	return ctx, packer
}

// dummyUser is a User service that returns a key for a given user.
type dummyUser struct {
	// The two slices go together
	userToMatch  []upspin.UserName
	keyToReturn  []upspin.PublicKey
	returnedKeys int
}

var _ upspin.User = (*dummyUser)(nil)

func (d *dummyUser) Lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	for i, u := range d.userToMatch {
		if u == userName {
			d.returnedKeys++
			return nil, []upspin.PublicKey{d.keyToReturn[i]}, nil
		}
	}
	return nil, nil, errors.New("user not found")
}
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	return d, nil
}
func (d *dummyUser) ServerUserName() string {
	return "dummyUser"
}
