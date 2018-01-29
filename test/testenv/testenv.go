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
	"strings"

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
	dirserver_inprocess "upspin.io/dir/inprocess"
	dirserver_server "upspin.io/dir/server"
	keyserver "upspin.io/key/inprocess"
	storeserver "upspin.io/store/inprocess"

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
	// OwnerName is the name of the user that runs the tests.
	OwnerName upspin.UserName

	// Kind is what kind of servers to use, "inprocess", "server", or "remote".
	Kind string

	// UpBox specifies whether to use upbox to run dirserver,
	// storeserver, and keyserver processes separate to the test process.
	// If false, the test server instances are run inside the test process.
	UpBox bool

	// Cache specifies whether to run a cacheserver for the owner.
	// This option applies only when UpBox is true.
	Cache bool

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

	keyServer   upspin.KeyServer
	storeServer upspin.StoreServer
	dirServer   upspin.DirServer

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
- name: %[2]q
  cache: %[3]t
servers:
- name: keyserver
  user: %[1]q
- name: storeserver
  user: %[1]q
- name: dirserver
  user: %[1]q
  flags:
    kind: %[4]s
domain: example.com
`

// New creates a new Env for testing.
func New(setup *Setup) (*Env, error) {
	const op errors.Op = "testenv.New"
	env := &Env{
		Setup: setup,
	}
	cfg := config.New()

	// All tests use the same keyserver, so that users of different
	// DirServers can still interact with each other.
	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{Transport: upspin.InProcess})

	switch setup.Kind {
	case "inprocess", "server":
		if setup.UpBox {
			// Use upbox.
			yaml := fmt.Sprintf(upboxYAML,
				TestServerName,
				setup.OwnerName,
				setup.Cache,
				setup.Kind,
			)
			schema, err := upbox.SchemaFromYAML(yaml)
			if err != nil {
				return nil, err
			}
			if err := schema.Start(); err != nil {
				return nil, err
			}
			env.schema = schema

			cfg, err = config.FromFile(schema.Config(string(TestServerName)))
			if err != nil {
				env.cleanup()
				return nil, err
			}
			env.Config = cfg
			break
		}

		// Test either the dir/inprocess or dir/server implementations
		// entire in-memory and offline.

		// Set endpoints.
		storeEndpoint := randomEndpoint("store")
		cfg = config.SetStoreEndpoint(cfg, storeEndpoint)
		dirEndpoint := randomEndpoint("dir")
		cfg = config.SetDirEndpoint(cfg, dirEndpoint)

		// Set up a StoreServer instance. Just use the inprocess
		// version for offline tests; the store/server implementation
		// isn't interesting when run offline.
		env.storeServer = storeserver.New()
		storeServerMux.Register(storeEndpoint, env.storeServer)

		// Set up user and factotum.
		cfg = config.SetUserName(cfg, TestServerName)
		f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", TestServerName[:strings.Index(TestServerName, "@")]))
		if err != nil {
			return nil, errors.E(op, err)
		}
		cfg = config.SetFactotum(cfg, f)

		// Set up DirServer instance.
		switch setup.Kind {
		case "inprocess":
			env.dirServer = dirserver_inprocess.New(cfg)
		case "server":
			// Create temporary directory for DirServer storage.
			logDir, err := ioutil.TempDir("", "testenv-dirserver")
			if err != nil {
				return nil, errors.E(op, err)
			}
			env.tmpDir = logDir
			env.dirServer, err = dirserver_server.New(cfg, "logDir="+logDir)
			if err != nil {
				env.cleanup()
				return nil, errors.E(op, err)
			}
		}
		dirServerMux.Register(dirEndpoint, env.dirServer)

		env.Config = cfg

	case "remote":
		if setup.UpBox {
			return nil, errors.E(op, "UpBox set with incompatible Kind (remote)")
		}

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

	default:
		return nil, errors.E(op, errors.Errorf("bad kind %q", setup.Kind))
	}

	cfg, err := env.NewUser(setup.OwnerName)
	if err != nil {
		env.cleanup()
		return nil, errors.E(op, err)
	}
	env.Config = cfg

	if err := makeRootIfNotExist(env.Config); err != nil {
		env.cleanup()
		return nil, errors.E(op, err)
	}

	env.Client = client.New(cfg)
	return env, nil
}

// Exit indicates the end of the test environment. It must only be called once. If Setup.Cleanup exists it is called.
func (e *Env) Exit() error {
	const op errors.Op = "testenv.Exit"

	if e.exitCalled {
		return errors.E(op, errors.Invalid, "exit already called")
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

	if e.dirServer != nil {
		e.dirServer.Close()
	}
	if e.storeServer != nil {
		e.storeServer.Close()
	}
	if e.keyServer != nil {
		e.keyServer.Close()
	}

	check(e.cleanup())

	return firstErr
}

func (e *Env) cleanup() error {
	var err error
	if e.tmpDir != "" {
		err = os.RemoveAll(e.tmpDir)
		e.tmpDir = ""
	}
	if e.schema != nil {
		s := e.schema
		e.schema = nil
		err2 := s.Stop()
		if err == nil {
			err = err2
		}
	}
	return err
}

// NewUser creates a new client for a user.  The new user will not
// have a root created. Callers should use the client to make a root directory if
// necessary.
func (e *Env) NewUser(userName upspin.UserName) (upspin.Config, error) {
	const op errors.Op = "testenv.NewUser"

	if e.Setup.UpBox {
		switch userName {
		case e.Setup.OwnerName, TestServerName:
			return config.FromFile(e.schema.Config(string(userName)))
		}
	}

	cfg := config.SetUserName(e.Config, userName)
	cfg = config.SetCacheEndpoint(cfg, upspin.Endpoint{})
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
	if errors.Is(errors.Exist, err) {
		return nil
	}
	return err
}
