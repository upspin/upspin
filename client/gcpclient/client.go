// Package gcpclient implements a simple client service talking to services
// running on GCP. User service is pre-loaded with a few test keys for now.
// TODO: make this more robust.
package gcpclient

import (
	"fmt"
	"log"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/client/common/file"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
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

// New creates a Client for talking to GCP. The client finds the
// servers according to the given Context.
func New(context *upspin.Context) upspin.Client {
	return &Client{
		context: context,
	}
}

// NewForTesting creates a new upspin.Client talking to the GCP
// backends located at storeURL and dirURL and the User service
// pre-loaded with the given user keys.
func NewForTesting(storeURL string, dirURL string, userKeys []UserKeys) upspin.Client {
	context := &upspin.Context{
		Packing:    upspin.UnsafePack,
		UserName:   upspin.UserName("edpin@google.com"),
		PrivateKey: upspin.PrivateKey("Zee Kee"),
	}

	context.User = newUser(context)
	context.Store = newStore(context, storeURL)
	context.Directory = newDirectory(context, dirURL)

	// TODO: this is a hack.
	testuser := context.User.(*testuser.Service)
	for _, uk := range userKeys {
		testuser.SetPublicKeys(uk.User, []upspin.PublicKey{uk.Public})
	}

	return New(context)
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
	// TODO: Some packers don't update the meta in PackLen, but some do. If not done, update it now.
	if len(meta.PackData) == 0 {
		meta.PackData = make(upspin.PackData, 1)
		meta.PackData[0] = byte(c.context.Packing)
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
	dir, err := c.getRootDir(dirName)
	if err != nil {
		return zeroLoc, err
	}
	return dir.MakeDirectory(dirName)
}

func (c *Client) getRootDir(name upspin.PathName) (upspin.Directory, error) {
	// Add a final slash in case it's just a user name and we're referencing the root.
	parsed, err := path.Parse(name + "/")
	if err != nil {
		return nil, err
	}
	endpoints, _, err := c.context.User.Lookup(parsed.User)
	if err != nil {
		return nil, err
	}
	var dir upspin.Directory
	for _, e := range endpoints {
		dir, err = access.BindDirectory(c.context, e)
		if dir != nil {
			return dir, nil
		}
	}
	if err == nil {
		err = fmt.Errorf("gcpclient: no such user %q", parsed.User)
	}
	return nil, err
}

func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	dir, err := c.getRootDir(name)
	if err != nil {
		return nil, err
	}
	entry, err := dir.Lookup(name)
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
	packer := pack.Lookup(entry.Location.Reference.Packing)
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
	dir, err := c.getRootDir(upspin.PathName(pattern))
	if err != nil {
		return nil, err
	}
	return dir.Glob(pattern)
}

func (c *Client) Create(name upspin.PathName) (upspin.File, error) {
	return file.Writable(c, name), nil
}
func (c *Client) Open(name upspin.PathName) (upspin.File, error) {
	data, err := c.Get(name)
	if err != nil {
		return nil, err
	}
	return file.Readable(c, name, data), nil
}
