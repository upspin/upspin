// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package merkle_test

import (
	"upspin.io/dir/merkle"
	"upspin.io/upspin"
)

func ExampleMerkle() {
	loader := func(loc upspin.Location) ([]byte, error) {
		// Load from disk...
		return nil, nil
	}
	saver := func(blob []byte, e upspin.Endpoint) (upspin.Location, error) {
		// Save to disk...
		return upspin.Location{}, nil
	}
	config := &merkle.Config{
		Loader: loader,
		Saver:  saver,
		Log:    merkle.NewLog( /* params for user, probably a local pathname ("/log/user@domain.com/") */ ),
	}
	userTree := merkle.New("user@domain.com", config)

	// Add to the user's root
	deRoot := &upspin.DirEntry{
		Name: "user@domain.com/",
		/* etc */
	}
	err := userTree.Add(deRoot, nil)
	if err != nil {
		panic(err)
	}

	// Add a new file.
	deFile := &upspin.DirEntry{
		Name: "user@domain.com/file1.txt",
		/* etc */
	}
	err = userTree.Add(deFile, deRoot)
	if err != nil {
		panic(err)
	}
	userTree.Shutdown() // Commit to disk (will call saver).

	root := userTree.RootLoc()
	// Stash the root location somewhere.

	// ...

	// Later, if tree is evicted, re-load the user from its root.
	newConfig := &merkle.Config{
		Loader: loader,
		Saver:  saver,
		Log:    merkle.NewLog( /* params for user@domain.com */ ),
	}
	reloadedTree := merkle.Load(root, newConfig)

	err = reloadedTree.Remove("user@domain.com/file1.txt")
	if err != nil {
		panic(err)
	}
}
