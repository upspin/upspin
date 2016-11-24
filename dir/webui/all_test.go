// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package webui

import (
	"fmt"
	"testing"
	"time"

	"upspin.io/test/testenv"
	"upspin.io/upspin"
)

const (
	user    = "bob@example.com" // bob has keys in key/testdata/bob.
	newUser = "carla@writer.io" // carla has keys in key/testdata/carla.
)

func TestPublishUnpublish(t *testing.T) {
	const (
		base           = user + "/pubunpub"
		publicDir      = base + "/public"
		privateDir     = base + "/private"
		accessFile     = publicDir + "/Access"
		accessContents = "r,l: all\n*:" + user
	)
	r, server := setup(t)
	r.As(user)
	r.MakeDirectory(base)
	r.MakeDirectory(publicDir)
	r.MakeDirectory(privateDir)
	r.Put(accessFile, accessContents)
	if r.Failed() {
		t.Fatal(r.Diag())
	}
	if len(server.trees) != 1 {
		t.Fatal("expected one")
	}
	time.Sleep(time.Second)
	fmt.Printf("root=%s kids: %d\n", server.trees[0].root.name, len(server.trees[0].root.kids))
}

func setup(t *testing.T) (*testenv.Runner, *Server) {
	env, err := testenv.New(&testenv.Setup{
		OwnerName: user,
		Packing:   upspin.PlainPack,
		Kind:      "server",
	})
	if err != nil {
		t.Fatal(err)
	}
	env.DirServer, err = WrapDir(env.DirServer, []upspin.PathName{user + "/"})
	if err != nil {
		t.Fatal(err)
	}
	r := testenv.NewRunner()
	r.AddUser(env.Context)
	return r, env.DirServer.(*Server)
}
