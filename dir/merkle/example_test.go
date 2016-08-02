// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package merkle_test

import (
	"upspin.io/dir/merkle"
	"upspin.io/upspin"
)

func ExampleMerkle() {
	var store upspin.StoreServer
	// TODO: initialize store

	config := &merkle.Config{
		Store: store,
		Log:   merkle.NewLog( /* params for user, probably a local pathname ("/log/user@domain.com/") */ ),
	}
	userTree := merkle.New("user@domain.com", config)

	// Add to the user's root
	deRoot := &upspin.DirEntry{
		Name: "user@domain.com/",
		/* etc */
	}
	err := userTree.Put(deRoot)
	if err != nil {
		panic(err)
	}

	// Add a new file.
	deFile := &upspin.DirEntry{
		Name: "user@domain.com/file1.txt",
		/* etc */
	}
	err = userTree.Put(deFile)
	if err != nil {
		panic(err)
	}
	userTree.Close() // Commit to disk (will call saver).

	root := userTree.Root()
	// Stash the root location somewhere.

	// ...

	// Later, if tree is evicted, re-load the user from its root.
	newConfig := &merkle.Config{
		Log: merkle.NewLog( /* params for user@domain.com */ ),
	}
	reloadedTree := merkle.Load(root, newConfig)

	err = reloadedTree.Delete("user@domain.com/file1.txt")
	if err != nil {
		panic(err)
	}
}
