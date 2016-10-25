// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testenv provides a declarative environment for creating a complete Upspin test tree.
// See testenv_test.go for an example on how to use it.
package testenv

import (
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/test/servermux"
	"upspin.io/upspin"
	"upspin.io/user"

	// Implementations that are instantiated explicitly by New.
	dirserver_inprocess "upspin.io/dir/inprocess"
	dirserver_server "upspin.io/dir/server"
	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"

	// Transports that are selected implicitly by bind.
	_ "upspin.io/dir/remote"
	_ "upspin.io/store/remote"

	// Transport required to use the remote store server.
	_ "upspin.io/store/https"
)

// The servers that "remote" tests will work against.
const (
	TestStoreServer = "store.test.upspin.io:443"
	TestDirServer   = "dir.test.upspin.io:443"
	TestServerName  = "dir-server@upspin.io"
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

	KeyServer   upspin.KeyServer
	StoreServer upspin.StoreServer
	DirServer   upspin.DirServer

	tmpDir     string
	exitCalled bool
}

var (
	storeServerMux *servermux.Mux
	dirServerMux   *servermux.Mux
)

func init() {
	var store upspin.StoreServer
	storeServerMux, store = servermux.NewStore()
	bind.RegisterStoreServer(upspin.InProcess, store)

	var dir upspin.DirServer
	dirServerMux, dir = servermux.NewDir()
	bind.RegisterDirServer(upspin.InProcess, dir)

	bind.RegisterKeyServer(upspin.InProcess, keyserver.New())
}

func randomEndpoint(prefix string) upspin.Endpoint {
	b := make([]byte, 64)
	rand.Read(b)
	return upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   upspin.NetAddr(fmt.Sprintf("%s-%x", prefix, b)),
	}
}

// New creates a new Env for testing.
func New(setup *Setup) (*Env, error) {
	const op = "testenv.New"
	env := &Env{
		Setup: setup,
	}
	ctx := context.New()

	// All tests use the same keyserver, so that users of different
	// DirServers can still interact with each other.
	ctx = context.SetKeyEndpoint(ctx, upspin.Endpoint{Transport: upspin.InProcess})

	switch k := setup.Kind; k {
	case "inprocess", "server":
		// Test either the dir/inprocess or dir/server implementations
		// entire in-memory and offline.

		// Set endpoints.
		storeEndpoint := randomEndpoint("store")
		ctx = context.SetStoreEndpoint(ctx, storeEndpoint)
		dirEndpoint := randomEndpoint("dir")
		ctx = context.SetDirEndpoint(ctx, dirEndpoint)

		// Set up a StoreServer instance. Just use the inprocess
		// version for offline tests; the store/gcp implementation
		// isn't interesting when run offline.
		env.StoreServer = storeserver.New()
		storeServerMux.Register(storeEndpoint, env.StoreServer)

		// Set up DirServer instance.
		switch k {
		case "inprocess":
			env.DirServer = dirserver_inprocess.New(ctx)
		case "server":
			// Set up user and factotum.
			ctx = context.SetUserName(ctx, TestServerName)
			f, err := factotum.NewFromDir(repo("key/testdata/" + TestServerName[:strings.Index(TestServerName, "@")]))
			if err != nil {
				return nil, errors.E(op, err)
			}
			ctx = context.SetFactotum(ctx, f)

			// Create temporary directory for DirServer storage.
			logDir, err := ioutil.TempDir("", "testenv-dirserver")
			if err != nil {
				return nil, errors.E(op, err)
			}
			env.tmpDir = logDir
			env.DirServer, err = dirserver_server.New(ctx, "logDir="+logDir)
			if err != nil {
				env.rmTmpDir()
				return nil, errors.E(op, err)
			}
		}
		dirServerMux.Register(dirEndpoint, env.DirServer)

	case "remote":
		ctx = context.SetStoreEndpoint(ctx, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   TestStoreServer,
		})
		ctx = context.SetDirEndpoint(ctx, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   TestDirServer,
		})

	default:
		return nil, errors.E(op, errors.Errorf("bad kind %q", k))
	}

	// Set the context to use the endpoints we created above.
	env.Context = ctx

	// Create a testuser, and set the context to the one for the user.
	ctx, err := env.NewUser(setup.OwnerName)
	if err != nil {
		env.rmTmpDir()
		return nil, errors.E(op, err)
	}
	env.Context = ctx

	if err := makeRootIfNotExist(ctx); err != nil {
		env.rmTmpDir()
		return nil, errors.E(op, err)
	}

	env.Client = client.New(ctx)
	return env, nil
}

// Exit indicates the end of the test environment. It must only be called once. If Setup.Cleanup exists it is called.
func (e *Env) Exit() error {
	const op = "testenv.Exit"

	if e.exitCalled {
		return errors.E(op, errors.Invalid, errors.Str("exit already called"))
	}
	e.exitCalled = true

	var firstErr error
	check := func(err error) {
		if err == nil {
			return
		}
		if firstErr == nil {
			firstErr = err
		}
		log.Debug.Println(op, err)
	}

	if e.Setup.Cleanup != nil {
		check(e.Setup.Cleanup(e))
	}

	check(e.rmTmpDir())

	if e.DirServer != nil {
		e.DirServer.Close()
	}
	if e.StoreServer != nil {
		e.StoreServer.Close()
	}
	if e.KeyServer != nil {
		e.KeyServer.Close()
	}

	return firstErr
}

func (e *Env) rmTmpDir() error {
	if e.tmpDir == "" {
		return nil
	}
	d := e.tmpDir
	e.tmpDir = ""
	return os.RemoveAll(d)
}

// NewUser creates a new client for a user.  The new user will not
// have a root created. Callers should use the client to make a root directory if
// necessary.
func (e *Env) NewUser(userName upspin.UserName) (upspin.Context, error) {
	const op = "testenv.NewUser"
	ctx := context.SetUserName(e.Context, userName)
	ctx = context.SetPacking(ctx, e.Setup.Packing)

	// Set up a factotum for the user.
	user, _, _, err := user.Parse(userName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	f, err := factotum.NewFromDir(repo("key/testdata/" + string(user)))
	if err != nil {
		return nil, errors.E(op, userName, err)
	}
	ctx = context.SetFactotum(ctx, f)

	// Register the user with the key server.
	err = registerUserWithKeyServer(ctx, ctx.UserName())
	if err != nil {
		return nil, errors.E(op, err)
	}

	return ctx, nil
}

// registerUserWithKeyServer registers userName's context with the inProcess keyServer.
func registerUserWithKeyServer(ctx upspin.Context, userName upspin.UserName) error {
	key, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		return err
	}
	// Install the registered user.
	user := &upspin.User{
		Name:      userName,
		Dirs:      []upspin.Endpoint{ctx.DirEndpoint()},
		Stores:    []upspin.Endpoint{ctx.StoreEndpoint()},
		PublicKey: ctx.Factotum().PublicKey(),
	}
	if err := key.Put(user); err != nil {
		return err
	}
	return nil
}

func makeRootIfNotExist(ctx upspin.Context) error {
	path := upspin.PathName(ctx.UserName()) + "/"
	dir, err := bind.DirServer(ctx, ctx.DirEndpoint())
	if err != nil {
		return err
	}

	entry := &upspin.DirEntry{
		Name:       path,
		SignedName: path,
		Attr:       upspin.AttrDirectory,
	}
	_, err = dir.Put(entry)
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
