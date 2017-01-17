// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gobind provides experimental Go bindings for a simplified
// Upspin Client and related data structures in such a way that languages
// such as Java and Objective-C can handle and gomobile can export.
// Currently, gomobile cannot export slices other than byte, so slices are
// converted to linked lists when necessary. Unsigned types are not supported
// either, so they're converted to their signed equivalents and truncations
// or roundings are silently ignored.
// This package is experimental and is NOT an official upspin.Client
// implementation. Its definition may change or break without warning.
package gobind

// To regenerate the .aar archive for Android Java, run:
//	go generate

//go:generate gomobile bind -target android upspin.io/client/gobind

import (
	"upspin.io/client"
	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/user"

	// Load everything we need.
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/transports"
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
func NewClient(clientConfig *ClientConfig) (*Client, error) {
	userName, err := user.Clean(upspin.UserName(clientConfig.UserName))
	if err != nil {
		return nil, err
	}
	cfg := config.New()
	cfg = config.SetUserName(cfg, userName)
	cfg = config.SetPacking(cfg, upspin.EEPack)
	f, err := factotum.NewFromKeys([]byte(clientConfig.PublicKey), []byte(clientConfig.PrivateKey), nil)
	if err != nil {
		log.Error.Printf("Error creating factotum: %s", err)
		return nil, err
	}
	cfg = config.SetFactotum(cfg, f)
	se := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(clientConfig.StoreNetAddr),
	}
	cfg = config.SetStoreEndpoint(cfg, se)
	de := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(clientConfig.DirNetAddr),
	}
	cfg = config.SetDirEndpoint(cfg, de)
	ue := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(clientConfig.KeyNetAddr),
	}
	cfg = config.SetKeyEndpoint(cfg, ue)
	return &Client{
		c: client.New(cfg),
	}, nil
}
