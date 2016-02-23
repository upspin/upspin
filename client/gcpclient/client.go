// Package gcpclient implements a simple client service talking to services
// running on GCP. User service is pre-loaded with a few test keys for now.
// TODO: make this more robust.
package gcpclient

import (
	"errors"
	"fmt"
	"log"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"

	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/pack/unsafe"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
)

type Client struct {
	context *upspin.Context
}

var _ upspin.Client = (*Client)(nil)

// UserKeys is a hack to allow us to instantiate a testuser pre-loaded
// with users and keys.
type UserKeys struct {
	User   upspin.UserName
	Public upspin.PublicKey
}

var (
	zeroLoc upspin.Location
)

// New creates a new upspin.Client talking to the GCP backends located
// at storeURL and dirURL and the User service pre-loaded with the
// given user keys.
// TODO: take a Context instead.
func New(storeURL string, dirURL string, userKeys []UserKeys) upspin.Client {
	client := Client{
		context: &upspin.Context{
			Packing:    upspin.UnsafePack,
			UserName:   upspin.UserName("edpin@google.com"),
			PrivateKey: upspin.PrivateKey("Zee Kee"),
		},
	}
	client.context.User = newUser(client.context)
	client.context.Store = newStore(client.context, storeURL)
	client.context.Directory = newDirectory(client.context, dirURL)

	// TODO: this is a hack.
	testuser := client.context.User.(*testuser.Service)
	for _, uk := range userKeys {
		testuser.SetPublicKeys(uk.User, []upspin.PublicKey{uk.Public})
	}
	return &client
}

// newUser creates a new in-process upspin.User client.
func newUser(context *upspin.Context) upspin.User {
	if context == nil {
		return nil
	}
	e := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	u, err := access.BindUser(context, e)
	if err != nil {
		log.Fatalf("Can't bind to User: %v", err)
	}
	return u
}

// newStore creates a new upspin.Store client for talking to a GCP
// server located at storeURL
func newStore(context *upspin.Context, storeURL string) upspin.Store {
	if context == nil {
		return nil
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(storeURL),
	}
	s, err := access.BindStore(context, e)
	if err != nil {
		log.Fatalf("Can't bind to Store: %v", err)
	}
	return s
}

// newDirectory creates a new upspin.Directory client for talking to a GCP
// server located at dirURL
func newDirectory(context *upspin.Context, dirURL string) upspin.Directory {
	if context == nil {
		return nil
	}
	if context.Store == nil {
		log.Fatal("Need a Store to initialize a Directory.")
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(dirURL),
	}
	d, err := access.BindDirectory(context, e)
	if err != nil {
		log.Fatalf("Can't bind to Directory: %v", err)
	}
	return d
}

func (c *Client) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	// Encrypt data according to the preferred packer
	// TODO: Do a Lookup in the parent directory to find the overriding packer.
	packer := pack.Lookup(c.context.Packing)
	if packer == nil {
		return zeroLoc, fmt.Errorf("unrecognized Packing %d for %q", c.context.Packing, name)
	}
	meta := &upspin.Metadata{}
	// Get a buffer big enough for this data
	cipherLen := packer.PackLen(c.context, data, meta, name)
	if cipherLen < 0 {
		return zeroLoc, fmt.Errorf("PackLen failed for %q", name)
	}
	cipher := make([]byte, cipherLen)
	n, err := packer.Pack(c.context, cipher, data, meta, name)
	if err != nil {
		return zeroLoc, err
	}
	cipher = cipher[:n]

	// Store it.
	return c.context.Directory.Put(name, cipher, meta.PackData)
}

func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return c.context.Directory.MakeDirectory(dirName)
}

func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	// TODO: ask c.context.User where the root for the user is. Right now, it's all in c.context.Directory.
	entry, err := c.context.Directory.Lookup(name)
	if err != nil {
		return nil, err
	}
	// Get the blob from the store.
	cipher, locs, err := c.context.Store.Get(entry.Location.Reference.Key)
	if err != nil {
		return nil, err
	}
	if len(locs) > 0 {
		// TODO: support more than one redirection
		cipher, _, err = c.context.Store.Get(locs[0].Reference.Key)
	}
	// Encrypted data was found. Unpack it.
	// TODO: This should look into
	// entry.Location.Reference.Packing instead. But dir.Put does
	// not store the packing, since it doesn't know.
	packer := pack.Lookup(c.context.Packing)
	if packer == nil {
		return nil, fmt.Errorf("unrecognized Packing %d for %q", entry.Location.Reference.Packing, name)
	}
	clearLen := packer.UnpackLen(c.context, cipher, &entry.Metadata)
	if clearLen < 0 {
		return nil, fmt.Errorf("unpackLen failed for %q", name)
	}
	cleartext := make([]byte, clearLen)
	n, err := packer.Unpack(c.context, cleartext, cipher, &entry.Metadata, name)
	if err != nil {
		return nil, err
	}
	return cleartext[:n], nil
}

func (c *Client) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return c.context.Directory.Glob(pattern)
}

func (c *Client) Create(name upspin.PathName) (upspin.File, error) {
	return nil, errors.New("not implemented")
}
func (c *Client) Open(name upspin.PathName) (upspin.File, error) {
	return nil, errors.New("not implemented")
}
