package ee

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/factotum"
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
func packBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, d *upspin.DirEntry, text []byte) []byte {
	cipher := make([]byte, packer.PackLen(ctx, text, d))
	m, err := packer.Pack(ctx, cipher, text, d)
	if err != nil {
		t.Fatal("Pack: ", err)
	}
	return cipher[:m]
}

// unpackBlob unpacks cipher according to the parameters and returns the plain text.
func unpackBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, d *upspin.DirEntry, cipher []byte) []byte {
	clear := make([]byte, packer.UnpackLen(ctx, cipher, d))
	m, err := packer.Unpack(ctx, clear, cipher, d)
	if err != nil {
		t.Fatal("Unpack: ", err)
	}
	return clear[:m]
}

// shareBlob updates the packdata of a blob such that the public keys given are readers of the blob.
func shareBlob(t *testing.T, ctx *upspin.Context, packer upspin.Packer, readers []upspin.PublicKey, packdata *[]byte) {
	pd := make([]*[]byte, 1)
	pd[0] = packdata
	packer.Share(ctx, readers, pd)
}

func testPackAndUnpack(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{}
	d.Name = name
	cipher := packBlob(t, ctx, packer, d, text)

	// Now unpack.
	clear := unpackBlob(t, ctx, packer, d, cipher)

	if !bytes.Equal(text, clear) {
		t.Errorf("text: expected %q; got %q", text, clear)
	}
}

func testPackNameAndUnpack(t *testing.T, ctx *upspin.Context, packer upspin.Packer, name, newName upspin.PathName, text []byte) {
	// First pack.
	d := &upspin.DirEntry{}
	d.Name = name
	cipher := packBlob(t, ctx, packer, d, text)

	// Name to newName.
	if err := packer.Name(ctx, d, newName); err != nil {
		t.Errorf("Name failed: %s", err)
	}
	if d.Name != newName {
		t.Errorf("Name failed to set the name")
	}

	// Now unpack.
	clear := unpackBlob(t, ctx, packer, d, cipher)

	if !bytes.Equal(text, clear) {
		t.Errorf("text: expected %q; got %q", text, clear)
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

func TestName256(t *testing.T) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.256")
		newName                 = upspin.PathName(user + "/file/of/user.256.2")
		text                    = "this is some text 256"
		packing                 = upspin.EEp256Pack
	)
	ctx, packer := setup(user, packing)
	testPackNameAndUnpack(t, ctx, packer, name, newName, []byte(text))
}

func TestName521(t *testing.T) {
	const (
		user    upspin.UserName = "user@google.com"
		name                    = upspin.PathName(user + "/file/of/user.521")
		name2                   = upspin.PathName(user + "/file/of/user.521.2")
		text                    = "this is some text 521"
		packing                 = upspin.EEp521Pack
	)
	ctx, packer := setup(user, packing)
	testPackNameAndUnpack(t, ctx, packer, name, name2, []byte(text))
}

func benchmarkPack(b *testing.B, packing upspin.Packing) {
	const (
		user upspin.UserName = "user@google.com"
		text                 = "this is some text"
	)
	name := upspin.PathName(fmt.Sprintf("%s/file/of/user.%d", user, packing))
	ctx, packer := setup(user, packing)
	for i := 0; i < b.N; i++ {
		d := &upspin.DirEntry{}
		d.Name = name
		data := []byte(text)
		cipher := make([]byte, packer.PackLen(ctx, data, d))
		m, _ := packer.Pack(ctx, cipher, data, d)
		cipher = cipher[:m]
		clear := make([]byte, packer.UnpackLen(ctx, cipher, d))
		m, _ = packer.Unpack(ctx, clear, cipher, d)
		clear = clear[:m]
	}
}

func BenchmarkPack256(b *testing.B) { benchmarkPack(b, upspin.EEp256Pack) }
func BenchmarkPack384(b *testing.B) { benchmarkPack(b, upspin.EEp384Pack) }
func BenchmarkPack521(b *testing.B) { benchmarkPack(b, upspin.EEp521Pack) }

func TestSharing(t *testing.T) {
	// dude@google.com is the owner of a file that is shared with bob@foo.com.
	const (
		dudesUserName upspin.UserName = "dude@google.com"
		packing                       = upspin.EEp256Pack
		pathName                      = upspin.PathName(dudesUserName + "/secret_file_shared_with_bob")
		bobsUserName  upspin.UserName = "bob@foo.com"
		text                          = "bob, here's the secret file. Sincerely, The Dude."
	)
	dudesKeyPair := upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934\n"),
	}
	bobsKeyPair := upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528"),
		Private: upspin.PrivateKey("93177533964096447201034856864549483929260757048490326880916443359483929789924"),
	}

	// Set up Dude as the creator/owner.
	ctx, packer := setup(dudesUserName, packing)
	// Set up a mock user service that knows about Dude's public keys (for checking signature during unpack).
	mockUser := &dummyUser{
		userToMatch: []upspin.UserName{dudesUserName},
		keyToReturn: []upspin.PublicKey{dudesKeyPair.Public},
	}
	f, err := factotum.New(dudesKeyPair)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f // Override setup to prevent reading keys from .ssh/
	ctx.User = mockUser

	d := &upspin.DirEntry{
		Name: pathName,
	}
	cipher := packBlob(t, ctx, packer, d, []byte(text))
	// Share with Bob
	shareBlob(t, ctx, packer, []upspin.PublicKey{bobsKeyPair.Public}, &d.Metadata.Packdata)

	// Now load Bob as the current user.
	ctx.UserName = bobsUserName
	f, err = factotum.New(bobsKeyPair)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f

	clear := unpackBlob(t, ctx, packer, d, cipher)
	if string(clear) != text {
		t.Errorf("Expected %s, got %s", text, clear)
	}

	// Finally, check that unpack looked up Dude's public key, to verify the signature.
	if mockUser.returnedKeys != 1 {
		t.Fatal("Packer failed to request dude's public key")
	}
}

func TestBadSharing(t *testing.T) {
	// dudette@google.com is the owner of a file that is attempting to be shared with mia@foo.com, but share wasn't called.
	const (
		dudettesUserName upspin.UserName = "dudette@google.com"
		packing                          = upspin.EEp256Pack
		pathName                         = upspin.PathName(dudettesUserName + "/secret_file_shared_with_mia")
		miasUserName     upspin.UserName = "mia@foo.com"
		text                             = "mia, here's the secret file. sincerely, dudette."
	)
	dudettesKeyPair := upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
	miasKeyPair := upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n22501350716439586308300487995594907386227865907589820632958610970814693581908\n104071495646780593180743128812641149143422089655848205222288250096821814372528"),
		Private: upspin.PrivateKey("93177533964096447201034856864549483929260757048490326880916443359483929789924"),
	}

	ctx, packer := setup(dudettesUserName, packing)
	mockUser := &dummyUser{
		userToMatch: []upspin.UserName{miasUserName, dudettesUserName},
		keyToReturn: []upspin.PublicKey{miasKeyPair.Public, dudettesKeyPair.Public},
	}
	f, err := factotum.New(dudettesKeyPair) // Override setup to prevent reading keys from .ssh/
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f
	ctx.User = mockUser

	d := &upspin.DirEntry{
		Name: pathName,
	}
	cipher := packBlob(t, ctx, packer, d, []byte(text))

	// Don't share with Mia (do nothing).

	// Now load Mia as the current user.
	ctx.UserName = miasUserName
	f, err = factotum.New(miasKeyPair)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Factotum = f

	// Mia can't unpack.
	clear := make([]byte, packer.UnpackLen(ctx, cipher, d))
	_, err = packer.Unpack(ctx, clear, cipher, d)
	if err == nil {
		t.Fatal("Expected error, got none.")
	}
	if !strings.Contains(err.Error(), "no wrapped key for me") {
		t.Fatalf("Expected no key error, got %s", err)
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
		panic("ecdsa.GenerateKey failed")
		// return ctx, packer
	}
	keyPair := upspin.KeyPair{
		Public:  upspin.PublicKey(fmt.Sprintf("%s\n%s\n%s\n", packer.String(), priv.X.String(), priv.Y.String())),
		Private: upspin.PrivateKey(fmt.Sprintf("%s\n", priv.D.String())),
	}
	ctx.Factotum, err = factotum.New(keyPair)
	if err != nil {
		panic("NewFactotum failed")
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
func (d *dummyUser) Dial(cc *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	return d, nil
}
func (d *dummyUser) ServerUserName() string {
	return "dummyUser"
}
func (d *dummyUser) Configure(options ...string) error {
	panic("unimplemented")
}
func (d *dummyUser) Endpoint() upspin.Endpoint {
	panic("unimplemented")
}
func (d *dummyUser) Ping() bool {
	return true
}
func (d *dummyUser) Shutdown() {
}
func (d *dummyUser) Authenticate(*upspin.Context) error {
	return nil
}
