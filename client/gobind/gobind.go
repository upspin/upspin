// Package gobind provides experimental Go bindings for a simplified
// Upspin Client and related data structures in such a way that languages
// such as Java and Objective-C can handle and gomobile can export.
// Currently, gomobile cannot export slices other than byte, so slices are
// converted to linked lists when necessary. Unsigned types are not supported
// either, so they're converted to their signed equivalents and truncations
// or roundings are silently ignored.
// This package is experimental and is NOT an official upspin.Client
// implementation. Its definition may change or break without warning.

// +build disabled

package gobind

// To regenerate the .aar archive for Android Java, run:
//	go generate

//go:generate gomobile bind -target android upspin.io/client/gobind

import (
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"

	// Load everything we need.
	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/store/transports"
)

// DirEntry represents the most relevant pieces of an upspin.DirEntry for clients.
type DirEntry struct {
	Name         string
	IsDir        bool
	Size         int64
	LastModified int64
	Writer       string
	Next         *DirEntry
}

// ClientConfig is a setup data structure that configures the Client
// for a given user with keys and server endpoints.
type ClientConfig struct {
	// UserName is the upspin.UserName.
	UserName string

	// PublicKey is the user's upspin.PublicKey.
	PublicKey string

	// PrivateKey is the user's private key.
	PrivateKey string

	// KeyNetAddr is the upspin.NetAddr of an upspin.Remote KeyServer endpoint.
	KeyNetAddr string

	// StoreNetAddr is the upspin.NetAddr of an upspin.Remote StoreServer endpoint.
	StoreNetAddr string

	// DirNetAddr is the upspin.NetAddr of an upspin.Remote DirServer endpoint.
	DirNetAddr string
}

// NewClientConfig returns a new ClientConfig.
func NewClientConfig() *ClientConfig {
	return new(ClientConfig)
}

// Client is a wrapped upspin.Client.
type Client struct {
	c upspin.Client
}

// Glob returns a linked list of DirEntry listing the results of the Glob operation.
func (c *Client) Glob(pattern string) (*DirEntry, error) {
	des, err := c.c.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var first *DirEntry
	var last *DirEntry
	for _, de := range des {
		size, err := de.Size()
		if err != nil {
			return nil, err
		}
		dirEntry := &DirEntry{
			Name:         string(de.Name),
			IsDir:        de.IsDir(),
			Size:         size,
			LastModified: int64(de.Time),
			Writer:       string(de.Writer),
		}
		if last != nil {
			last.Next = dirEntry
		} else {
			first = dirEntry
		}
		last = dirEntry
	}
	return first, nil
}

// Get returns the contents of a path.
func (c *Client) Get(path string) ([]byte, error) {
	return c.c.Get(upspin.PathName(path))
}

// Put puts the data as the contents of name and returns its reference in the default location (at the default store).
func (c *Client) Put(name string, data []byte) (string, error) {
	entry, err := c.c.Put(upspin.PathName(name), data)
	if err != nil {
		return "", err
	}
	if len(entry.Blocks) == 0 {
		return "<empty>", nil
	}
	return string(entry.Blocks[0].Location.Reference), nil // TODO: This should include all blocks.
}

// NewClient returns a new Client for a given user's configuration.
func NewClient(config *ClientConfig) (*Client, error) {
	ctx := context.New().SetUserName(upspin.UserName(config.UserName)).SetPacking(upspin.EEPack)
	f, err := factotum.DeprecatedNew(upspin.PublicKey(config.PublicKey), config.PrivateKey)
	if err != nil {
		log.Error.Printf("Error creating factotum: %s", err)
		return nil, err
	}
	ctx.SetFactotum(f)
	se := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(config.StoreNetAddr),
	}
	ctx.SetStoreEndpoint(se)
	de := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(config.DirNetAddr),
	}
	ctx.SetDirEndpoint(de)
	ue := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(config.KeyNetAddr),
	}
	ctx.SetKeyEndpoint(ue)
	return &Client{
		c: client.New(ctx),
	}, nil
}
