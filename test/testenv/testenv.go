// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testenv provides a declarative environment for creating a complete Upspin test tree.
// See testenv_test.go for an example on how to use it.
package testenv

import (
	"crypto/rand"
	"fmt"
	"strconv"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/test/servermux"
	"upspin.io/test/testutil"
	"upspin.io/upbox"
	"upspin.io/upspin"
	"upspin.io/user"

	// Implementations that are instantiated explicitly by New.

	keyserver "upspin.io/key/inprocess"

	// Transports that are selected implicitly by bind.
	_ "upspin.io/dir/remote"
	_ "upspin.io/key/remote"
	_ "upspin.io/store/remote"
)

// The servers that "remote" tests will work against.
const (
	TestKeyServer   = "key.test.upspin.io:443"
	TestStoreServer = "store.test.upspin.io:443"
	TestDirServer   = "dir.test.upspin.io:443"
	TestServerName  = "dir-server@upspin.io"
)

// Setup is a configuration structure that contains a directory tree and other optional flags.
type Setup struct {
	// OwnerName is the name of the directory tree owner.
	OwnerName upspin.UserName

	// Kind is what kind of servers to use, "inprocess", "server", or "remote".
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

	// Config is the configuration used when creating the client.
	Config upspin.Config

	// Setup contains the original setup options.
	Setup *Setup

	KeyServer   upspin.KeyServer
	StoreServer upspin.StoreServer
	DirServer   upspin.DirServer

	schema     *upbox.Schema
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

const upboxYAML = `
users:
- name: %[1]q
servers:
- name: keyserver
  user: %[1]q
- name: storeserver
  user: %[1]q
- name: dirserver
  user: %[1]q
  flags:
    kind: %[2]s
domain: example.com
`

// New creates a new Env for testing.
func New(setup *Setup) (*Env, error) {
	const op = "testenv.New"
	env := &Env{
		Setup: setup,
	}
	cfg := config.New()

	// All tests use the same keyserver, so that users of different
	// DirServers can still interact with each other.
	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{Transport: upspin.InProcess})

	switch k := setup.Kind; k {
	case "inprocess", "server":
		// Test either the dir/inprocess or dir/server implementations
		// entire in-memory and offline.

		portS, err := testutil.PickPort()
		if err != nil {
			return nil, err
		}
		port, _ := strconv.Atoi(portS)
		yaml := fmt.Sprintf(upboxYAML, setup.OwnerName, k)
		schema, err := upbox.SchemaFromYAML(yaml, port)
		if err != nil {
			return nil, err
		}
		if err := schema.Start(); err != nil {
			return nil, err
		}
		env.schema = schema

		cfg, err = config.FromFile(schema.Config(string(setup.OwnerName)))
		if err != nil {
			env.cleanup()
			return nil, err
		}
		env.Config = cfg

	case "remote":
		cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   TestKeyServer,
		})
		cfg = config.SetStoreEndpoint(cfg, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   TestStoreServer,
		})
		cfg = config.SetDirEndpoint(cfg, upspin.Endpoint{
			Transport: upspin.Remote,
			NetAddr:   TestDirServer,
		})
		env.Config = cfg

		cfg, err := env.NewUser(setup.OwnerName)
		if err != nil {
			env.cleanup()
			return nil, errors.E(op, err)
		}
		env.Config = cfg

	default:
		return nil, errors.E(op, errors.Errorf("bad kind %q", k))
	}

	if err := makeRootIfNotExist(cfg); err != nil {
		env.cleanup()
		return nil, errors.E(op, err)
	}

	env.Client = client.New(cfg)
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

	if e.DirServer != nil {
		e.DirServer.Close()
	}
	if e.StoreServer != nil {
		e.StoreServer.Close()
	}
	if e.KeyServer != nil {
		e.KeyServer.Close()
	}

	check(e.cleanup())

	return firstErr
}

func (e *Env) cleanup() error {
	if e.schema != nil {
		s := e.schema
		e.schema = nil
		return s.Stop()
	}
	return nil
}

// NewUser creates a new client for a user.  The new user will not
// have a root created. Callers should use the client to make a root directory if
// necessary.
func (e *Env) NewUser(userName upspin.UserName) (upspin.Config, error) {
	const op = "testenv.NewUser"
	cfg := config.SetUserName(e.Config, userName)
	cfg = config.SetPacking(cfg, e.Setup.Packing)

	// Set up a factotum for the user.
	user, _, _, err := user.Parse(userName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	var secrets string
	if e.Setup.Kind == "remote" {
		secrets = testutil.Repo("key", "testdata", "remote", string(user))
	} else {
		secrets = testutil.Repo("key", "testdata", string(user))
	}
	f, err := factotum.NewFromDir(secrets)
	if err != nil {
		return nil, errors.E(op, userName, err)
	}
	cfg = config.SetFactotum(cfg, f)

	// Don't register users with the test cluster key server;
	// our test users should be already registered there.
	if e.Setup.Kind != "remote" {
		// Register the user with the key server.
		err = registerUserWithKeyServer(e.Config, cfg)
		if err != nil {
			return nil, errors.E(op, err)
		}
	}

	return cfg, nil
}

// registerUserWithKeyServer registers userName's config with the inProcess keyServer.
func registerUserWithKeyServer(server upspin.Config, user upspin.Config) error {
	key, err := bind.KeyServer(server, server.KeyEndpoint())
	if err != nil {
		return err
	}
	// Install the registered user.
	u := &upspin.User{
		Name:      user.UserName(),
		Dirs:      []upspin.Endpoint{user.DirEndpoint()},
		Stores:    []upspin.Endpoint{user.StoreEndpoint()},
		PublicKey: user.Factotum().PublicKey(),
	}
	return key.Put(u)
}

func makeRootIfNotExist(cfg upspin.Config) error {
	path := upspin.PathName(cfg.UserName()) + "/"
	dir, err := bind.DirServer(cfg, cfg.DirEndpoint())
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
