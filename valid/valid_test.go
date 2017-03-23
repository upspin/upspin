// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package valid

import (
	"testing"

	"upspin.io/access"
	"upspin.io/upspin"
)

// We use path.UserAndDomain and expect it to do most of the testing for us.
// These are just a few simple tests, plus the lower-case check for the domain.
func TestUserName(t *testing.T) {
	tests := []struct {
		name  upspin.UserName
		valid bool
	}{
		{"", false},
		{"a@b.com/foo", false},
		{"a@b.com", true},
		{"a@b", false},
		{"A@b.com", true}, // User name is case sensitive.
		{"@b.com", false},
		{"a@b.c..com", false},
		{"a@b@c.com", false},
		{"a@c.%.com", false},
		{"a@CC.com", false}, // Domain must be lower case (this package only).
		{"a@cc.x", false},   // Final domain name must be >= 2 bytes.
		{access.AllUsers, false},
	}
	for _, test := range tests {
		err := UserName(test.name)
		if test.valid == (err == nil) {
			continue
		}
		t.Errorf("%q: expected valid=%t; got error %v", test.name, test.valid, err)
	}
}

func TestValidPathName(t *testing.T) {
	tests := []struct {
		name  upspin.PathName
		valid bool
	}{
		{"", false},
		{"a*b.com/foo", false},
		{"a@b.com", false}, // No trailing slash.
		{"a@b.com/", true},
		{"a@b.com//", false},
		{"a@b.com/foo", true},
		{"a@b.com/foo/bar/..", false},
	}
	for _, test := range tests {
		err := validPathName(test.name)
		if test.valid == (err == nil) {
			continue
		}
		t.Errorf("%q: expected valid=%t; got error %v", test.name, test.valid, err)
	}
}

func TestUser(t *testing.T) {
	var user upspin.User
	restore := func() {
		user = upspin.User{
			Name: "jamestiberius@kirk.com",
			Dirs: []upspin.Endpoint{
				{
					Transport: upspin.Remote,
					NetAddr:   "dir.upspin.io:443",
				},
			},
			Stores: []upspin.Endpoint{
				{
					Transport: upspin.Remote,
					NetAddr:   "store.upspin.io:443",
				},
			},
			PublicKey: "this is a key",
		}
	}
	restore()
	if err := User(&user); err != nil {
		t.Fatalf("expected no error; got %q", err)
	}
	// Bad name.
	user.Name = "joe@blow.com/file"
	if err := User(&user); err == nil {
		t.Fatal("no error for bad user")
	}
	// Bad dir.
	user.Dirs[0].Transport = 44
	if err := User(&user); err == nil {
		t.Fatal("no error for bad dir")
	}
	// Bad store.
	user.Stores[0].Transport = 44
	if err := User(&user); err == nil {
		t.Fatal("no error for bad store")
	}
}

func TestDirBlock(t *testing.T) {
	var block upspin.DirBlock
	restore := func() {
		block = upspin.DirBlock{
			Location: upspin.Location{
				Endpoint: upspin.Endpoint{
					Transport: upspin.Remote,
					NetAddr:   "store.upspin.io:443",
				},
				Reference: "a block",
			},
			Offset: 1234,
			Size:   12345,
		}
	}
	restore()
	if err := DirBlock(block); err != nil {
		t.Fatalf("expected no error; got %q", err)
	}
	// Negative size.
	block.Size = -1
	if err := DirBlock(block); err == nil {
		t.Fatal("no error for negative size")
	}
	restore()
	// Negative offset.
	block.Offset = -1
	if err := DirBlock(block); err == nil {
		t.Fatal("no error for negative offset")
	}
	restore()
	// Bad endpoint
	block.Location.Endpoint.Transport = 44
	if err := DirBlock(block); err == nil {
		t.Fatal("no error for bad transport")
	}
	restore()
	// Empty reference.
	block.Location.Reference = ""
	if err := DirBlock(block); err == nil {
		t.Fatal("no error for empty reference")
	}
}

func TestEndpoint(t *testing.T) {
	var endpoint upspin.Endpoint
	restore := func() {
		endpoint = upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   "store.upspin.io:443",
		}
	}
	restore()
	if err := Endpoint(endpoint); err != nil {
		t.Fatalf("expected no error; got %q", err)
	}
	// Bad transport
	endpoint.Transport = 44
	if err := Endpoint(endpoint); err == nil {
		t.Fatal("no error for bad transport")
	}
	restore()
	// Missing network address
	endpoint.NetAddr = ""
	if err := Endpoint(endpoint); err == nil {
		t.Fatal("no error for bad transport")
	}
	restore()
	// One last check for network address for unassigned.
	endpoint.Transport = upspin.Unassigned
	if err := Endpoint(endpoint); err == nil {
		t.Fatal("no error for network address with Unassigned transport")
	}
}

func TestDirEntry(t *testing.T) {
	block0 := upspin.DirBlock{
		Location: upspin.Location{
			Endpoint: upspin.Endpoint{
				Transport: upspin.Remote,
				NetAddr:   "store.upspin.io:443",
			},
			Reference: "a block",
		},
		Offset: 0,
		Size:   12345,
	}
	block1 := upspin.DirBlock{
		Location: upspin.Location{
			Endpoint: upspin.Endpoint{
				Transport: upspin.Remote,
				NetAddr:   "store.upspin.io:443",
			},
			Reference: "a block",
		},
		Offset: block0.Size,
		Size:   12345,
	}
	var entry upspin.DirEntry
	restore := func() {
		entry = upspin.DirEntry{
			Name:       "curly@stooges.com/boink",
			SignedName: "curly@stooges.com/boink",
			Packing:    upspin.PlainPack,
			Time:       upspin.Now(),
			Blocks:     []upspin.DirBlock{block0, block1},
			Packdata:   []byte("unused"),
			Link:       "",
			Writer:     "moe@stooges.com",
			Attr:       upspin.AttrNone,
			Sequence:   27,
		}
	}
	restore()
	if err := DirEntry(&entry); err != nil {
		t.Fatalf("expected no error; got %q", err)
	}
	// Bad name.
	entry.Name = "curly@stooges.com"
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for bad name")
	}
	restore()
	// Mismatched names.
	entry.SignedName = "curly@stooges.com/nyuk"
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for mismatched Name and SignedName")
	}
	restore()
	// Bad attribute.
	entry.Attr = upspin.AttrLink | upspin.AttrDirectory
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for bad attribute")
	}
	restore()
	// Data present for link
	entry.Attr = upspin.AttrLink
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for link with data")
	}
	restore()
	// Data present for directory.
	entry.Attr = upspin.AttrDirectory
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for directory with data")
	}
	restore()
	// Link present for non-link
	entry.Link = "moe@stooges.com/nyuk"
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for Link field set for non-link")
	}
	restore()
	// Bad packing.
	entry.Packing = 44
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for bad packing")
	}
	restore()
	// Bad sequence.
	entry.Sequence = -44
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for bad packing")
	}
	restore()
	// No writer.
	entry.Writer = ""
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for missing writer")
	}
	restore()
	// Badly-formatted writer.
	entry.Writer = "yo!"
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for badly-formatted writer")
	}
	restore()
	// Block overlap
	entry.Blocks[1].Offset--
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for overlapping blocks")
	}
	restore()
	// Hole in file.
	entry.Blocks[1].Offset++
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for overlapping blocks")
	}
	restore()
	// Zero-length block.
	entry.Blocks = append(entry.Blocks, upspin.DirBlock{})
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for zero-length block")
	}
	restore()
	// Invalid block.
	entry.Blocks[1].Location.Endpoint.Transport = 44
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for bad block")
	}
	restore()
	entry.Packing = upspin.UnassignedPack
	entry.Attr = upspin.AttrDirectory
	entry.Blocks = nil
	entry.Packdata = nil
	if err := DirEntry(&entry); err != nil {
		t.Fatalf("expected no error, got %s", err)
	}
	restore()
	entry.Packing = upspin.UnassignedPack
	if err := DirEntry(&entry); err == nil {
		t.Fatal("no error for unassigned pack for a file")
	}
	restore()
}

func TestReference(t *testing.T) {
	tests := []struct {
		ref   upspin.Reference
		valid bool
	}{
		{"", false}, // Empty string is invalid reference.
		{"5745647547654764567", true},
		{"日本語", true},
		{"abced\x80", false},   // Invalid UTF-8
		{"a\tb", false},        // Unprintable character.
		{"abded\uFFFD", false}, // Replacement rune is invalid.
	}
	for _, test := range tests {
		err := Reference(test.ref)
		if test.valid == (err == nil) {
			continue
		}
		t.Errorf("%q: expected valid=%t; got error %v", test.ref, test.valid, err)
	}
}
