// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testenv provides a declarative environment for creating a complete Upspin test tree.
// See testenv_test.go for an example on how to use it.
package testenv

import (
	"log"
	"os"
	"path/filepath"

	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/path"
	"upspin.io/upspin"

	// Potential transports, selected by the Setup's Kind field.
	_ "upspin.io/dir/inprocess"
	_ "upspin.io/dir/remote"
	_ "upspin.io/key/inprocess"
	_ "upspin.io/store/inprocess"
	_ "upspin.io/store/remote"
)

// Setup is a configuration structure that contains a directory tree and other optional flags.
type Setup struct {
	// OwnerName is the name of the directory tree owner.
	OwnerName upspin.UserName

	// Kind is what kind of servers to use, "inprocess" or "remote".
	Kind string

	// Packing is the desired packing for the tree.
	Packing upspin.Packing

	// Verbose indicates whether we should print verbose debug messages.
	Verbose bool

	// Cleanup, if present, is run at Exit to clean up any test state necessary.
	// It may return an error, which is returned by Exit.
	Cleanup func(e *Env) error
}

// Env is the test environment. It contains a client which is the main piece that tests should use.
type Env struct {
	// Client is the client tests should use for reaching the newly-created Tree.
	Client upspin.Client

	// Context is the context used when creating the client.
	Context upspin.Context

	// Setup contains the original setup options.
	Setup *Setup

	exitCalled bool
}

// New creates a new Env for testing.
func New(setup *Setup) (*Env, error) {
	const op = "testenv.New"
	env := &Env{
		Setup: setup,
	}
	ctx, err := env.NewUser(setup.OwnerName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	env.Context = ctx
	env.Client = client.New(ctx)
	if err := makeRootIfNotExist(ctx); err != nil {
		return nil, errors.E(op, err)
	}
	if setup.Verbose {
		log.Printf("Tree: All entries created.")
	}
	return env, nil
}

// Exit indicates the end of the test environment. It must only be called once. If Setup.Cleanup exists it is called.
func (e *Env) Exit() error {
	const op = "testenv.Exit"
	if e.exitCalled {
		return errors.E(op, errors.Invalid, errors.Str("exit already called"))
	}
	e.exitCalled = true
	if e.Setup.Cleanup != nil {
		err := e.Setup.Cleanup(e)
		if err != nil {
			return errors.E(op, err)
		}
	}
	return nil
}

// NewUser creates a new client for a user.  The new user will not
// have a root created. Callers should use the client to MakeDirectory if
// necessary.
func (e *Env) NewUser(userName upspin.UserName) (upspin.Context, error) {
	const op = "testenv.NewUser"
	ctx := context.New().SetUserName(userName).SetPacking(e.Setup.Packing)

	// Get keys for user.
	user, _, err := path.UserAndDomain(userName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	f, err := factotum.New(repo("key/testdata/" + string(user)))
	if err != nil {
		return nil, errors.E(op, userName, err)
	}
	ctx.SetFactotum(f)

	// Set up endpoints.
	inProcessEndpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	ctx.SetKeyEndpoint(inProcessEndpoint)

	switch k := e.Setup.Kind; k {
	case "remote":
		ctx.SetStoreEndpoint(upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   "store.test.upspin.io:443", // Test store server.
		})
		ctx.SetDirEndpoint(upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   "dir.test.upspin.io:443", // Test dir server.
		})
	case "inprocess":
		ctx.SetStoreEndpoint(inProcessEndpoint)
		ctx.SetDirEndpoint(inProcessEndpoint)
	default:
		return nil, errors.E(op, errors.Invalid, errors.Errorf("bad server kind %q", k))
	}

	// Register user with key server.
	err = registerUserWithKeyServer(userName, ctx)
	if err != nil {
		return nil, errors.E(op, err)
	}

	return ctx, nil
}

// registerUserWithKeyServer registers userName's context with the inProcess keyServer.
func registerUserWithKeyServer(userName upspin.UserName, context upspin.Context) error {
	key := context.KeyServer()
	// Install the registered user.
	user := &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{context.DirEndpoint()},
		Stores:    []upspin.Endpoint{context.StoreEndpoint()},
		PublicKey: context.Factotum().PublicKey(),
	}
	if err := key.Put(user); err != nil {
		return errors.E(err)
	}
	return nil
}

func makeRootIfNotExist(context upspin.Context) error {
	path := upspin.PathName(context.UserName()) + "/"
	dir := context.DirServer(path)
	_, err := dir.MakeDirectory(path)
	if err != nil && !errors.Match(errors.E(errors.Exist), err) {
		return err
	}
	return nil
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("test/testenv: no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
