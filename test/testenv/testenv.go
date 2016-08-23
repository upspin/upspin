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
	"strings"

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

// Entry is an entry in the Upspin namespace.
type Entry struct {
	// P is a path name in string format. Directories must end in "/".
	P string

	// C is the contents of P. C must be empty for directories.
	C string
}

// Setup is a configuration structure that contains a directory tree and other optional flags.
type Setup struct {
	// OwnerName is the name of the directory tree owner.
	OwnerName upspin.UserName

	// Kind is what kind of servers to use, "inprocess" or "GCP".
	Kind string

	// Packing is the desired packing for the tree.
	Packing upspin.Packing

	// Tree is the directory tree desired at the start of the test environment.
	Tree Tree

	// Some configuration options follow

	// Verbose indicates whether we should print verbose debug messages.
	Verbose bool

	// IgnoreExistingDirectories does not report an error if the directories already exist from a previous run.
	IgnoreExistingDirectories bool

	// Cleanup, if present, is run at Exit to clean up any test state necessary.
	// It may return an error, which is returned by Exit.
	Cleanup func(e *Env) error
}

// Tree is a full directory tree with path names and their contents.
type Tree []Entry

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
	client, context, err := env.NewUser(setup.OwnerName)
	if err != nil {
		return nil, err
	}
	env.Client = client
	env.Context = context
	err = makeRoot(context)
	if err != nil {
		return nil, err
	}
	// Generate the dir tree using the client.
	for _, e := range setup.Tree {
		if strings.HasSuffix(e.P, "/") {
			if len(e.C) > 0 {
				return nil, errors.E(op, errors.NotEmpty, errors.Str("directory entry must not have contents"))
			}
			dir := path.Join(upspin.PathName(setup.OwnerName), e.P)
			entry, err := client.MakeDirectory(dir)
			if err != nil {
				if !setup.IgnoreExistingDirectories {
					log.Printf("Tree: Error creating directory %s: %s", dir, err)
					return nil, err
				}
			}
			if setup.Verbose {
				log.Printf("Tree: Created directory %#v", entry)
			}
		} else {
			name := path.Join(upspin.PathName(setup.OwnerName), e.P)
			entry, err := client.Put(name, []byte(e.C))
			if err != nil {
				log.Printf("Error creating file %s: %s", name, err)
				return nil, errors.E(op, err)
			}
			if setup.Verbose {
				log.Printf("Tree: Created file %#v", entry)
			}
		}
	}
	if setup.Verbose {
		log.Printf("Tree: All entries created.")
	}
	return env, nil
}

// E (short for Entry) is a helper function to return a new Entry.
func E(pathName string, contents string) Entry {
	return Entry{
		P: pathName,
		C: contents,
	}
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
func (e *Env) NewUser(userName upspin.UserName) (upspin.Client, upspin.Context, error) {
	const op = "testenv.NewUser"
	var ctx upspin.Context
	var err error
	ctx = context.New().SetUserName(userName).SetPacking(e.Setup.Packing)
	// Get keys for user.
	user, _, err := path.UserAndDomain(userName)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}
	f, err := factotum.New(repo("key/testdata/" + string(user)))
	if err != nil {
		return nil, nil, errors.E(op, userName, err)
	}
	ctx.SetFactotum(f)

	var client upspin.Client
	switch k := e.Setup.Kind; k {
	case "gcp":
		client, err = gcpClient(ctx)
	case "inprocess":
		client, err = inProcessClient(ctx)
	default:
		return nil, nil, errors.E(op, errors.Invalid, errors.Errorf("bad server kind %q", k))
	}
	if err != nil {
		return nil, nil, errors.E(op, err)
	}

	// Register user with key server.
	err = registerUserWithKeyServer(userName, ctx)
	if err != nil {
		return nil, nil, errors.E(op, err)
	}

	return client, ctx, nil
}

// gcpClient returns a Client pointing to the GCP test instances on upspin.io given a Context partially initialized
// with a user and keys.
func gcpClient(context upspin.Context) (upspin.Client, error) {
	// Use a test GCP StoreServer...
	endpointStore := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   "store.test.upspin.io:443", // Test store server.
	}
	// ... and a test GCP directory ...
	endpointDir := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   "dir.test.upspin.io:443", // Test dir server.
	}
	// and an in-process test user.
	endpointKey := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	setContextEndpoints(context, endpointStore, endpointDir, endpointKey)
	client := client.New(context)
	return client, nil
}

// inProcessClient returns a Client pointing to in-process instances given a Context partially initialized
// with a user and keys.
func inProcessClient(context upspin.Context) (upspin.Client, error) {
	// Use an in-process StoreServer...
	endpointStore := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	// ... and an in-process directory ...
	endpointDir := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	// and an in-process test user.
	endpointKey := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	setContextEndpoints(context, endpointStore, endpointDir, endpointKey)
	client := client.New(context)
	return client, nil
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

func makeRoot(context upspin.Context) error {
	path := upspin.PathName(context.UserName()) + "/"
	dir := context.DirServer(path)
	// Make the root to be sure it's there.
	_, err := dir.MakeDirectory(path)
	if err != nil && !strings.Contains(err.Error(), "already ") {
		return err
	}
	return nil
}

func setContextEndpoints(context upspin.Context, store, dir, key upspin.Endpoint) {
	context.SetStoreEndpoint(store)
	context.SetDirEndpoint(dir)
	context.SetKeyEndpoint(key)
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("test/testenv: no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
