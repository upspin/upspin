// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package context creates a client context from various sources.
package context

import (
	"bufio"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/upspin"
)

var inTest = false // Generate errors instead of logs for certain problems.

// base implements upspin.Context, returning default values for all operations.
type base struct{}

func (base) UserName() upspin.UserName           { return defaultUserName }
func (base) Factotum() upspin.Factotum           { return nil }
func (base) Packing() upspin.Packing             { return defaultPacking }
func (base) KeyEndpoint() upspin.Endpoint        { return ep0 }
func (base) DirEndpoint() upspin.Endpoint        { return ep0 }
func (base) StoreEndpoint() upspin.Endpoint      { return ep0 }
func (base) StoreCacheEndpoint() upspin.Endpoint { return ep0 }

// New returns a context with all fields set as defaults.
func New() upspin.Context {
	return base{}
}

var (
	defaultUserName = upspin.UserName("noone@nowhere.org")
	defaultPacking  = upspin.PlainPack
)

// Known keys. All others are treated as errors.
const (
	username    = "username"
	keyserver   = "keyserver"
	dirserver   = "dirserver"
	storeserver = "storeserver"
	storecache  = "storecache"
	packing     = "packing"
	secrets     = "secrets"
)

// FromFile initializes a context using the given file.
// As with InitContext, environment variables may override the
// values in the context file.
func FromFile(name string) (upspin.Context, error) {
	f, err := os.Open(flags.Context)
	if err != nil {
		return nil, errors.E("context.FromFile", err)
	}
	defer f.Close()
	return InitContext(f)
}

// InitContext returns a context generated from a configuration file and/or
// environment variables.
//
// A configuration file should be of the format
//   # lines that begin with a hash are ignored
//   key = value
// where key may be one of username, keyserver, dirserver, storeserver,
// packing, or secrets.
//
// The default configuration file location is $HOME/upspin/rc.
// If passed a non-nil io.Reader, that is used instead of the default file.
//
// Environment variables named "upspinkey", where "key" is a recognized
// configuration key, may override configuration values in the rc file.
//
// Any endpoints (keyserver, dirserver, storeserver) not set in the data for
// the context will be set to the "unassigned" transport and an empty network
// address.
//
// The default value for packing is "plain".
//
// The default value for secrets is "$HOME/.ssh".
// The special value "none" indicates there are no secrets to load;
// in this case, the returned context will not include a Factotum
// and the returned error is ErrNoFactotum.
func InitContext(r io.Reader) (upspin.Context, error) {
	const op = "context.InitContext"
	vals := map[string]string{
		username:    string(defaultUserName),
		packing:     defaultPacking.String(),
		keyserver:   "",
		dirserver:   "",
		storeserver: "",
		storecache:  "",
		secrets:     "",
	}

	// If the provided reader is nil, try $HOME/upspin/rc.
	if r == nil {
		home, err := homedir()
		if err != nil {
			return nil, errors.E(op, errors.Errorf("cannot load keys: %v", err))
		}
		f, err := os.Open(filepath.Join(home, "upspin/rc"))
		if err != nil {
			return nil, errors.E(op, errors.Errorf("cannot load keys: %v", err))
		}
		r = f
		defer f.Close()
	}

	// First source of truth is the RC file.
	if r != nil {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			// Remove comments.
			if sharp := strings.IndexByte(line, '#'); sharp >= 0 {
				line = line[:sharp]
			}
			line = strings.TrimSpace(line)
			tokens := strings.SplitN(line, "=", 2)
			if len(tokens) != 2 {
				continue
			}
			val := strings.TrimSpace(tokens[1])
			attr := strings.TrimSpace(tokens[0])
			if _, ok := vals[attr]; !ok {
				return nil, errors.E(op, errors.Invalid, errors.Errorf("unrecognized key %q", attr))
			}
			vals[attr] = val
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	// Environment variables trump the RC file. Look for any "upspin" values and
	// warn about them - don't give errors though as they may be inconsequential.
	// (We do generate an error when testing.)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "upspin") {
			continue
		}
		// Variables we care about look like upspinkey=value.
		kv := strings.SplitN(v, "=", 2)
		if len(kv) != 2 {
			log.Printf("context: invalid environment variable %q ignored", v)
			continue
		}
		attr := kv[0][len("upspin"):]
		val := kv[1]
		if _, ok := vals[attr]; !ok {
			if inTest {
				return nil, errors.E(op, errors.Invalid, errors.Errorf("unrecognized environment variable %q", v))
			} else {
				log.Printf("context: unrecognized environment variable %q ignored", v)
			}
			continue
		}
		if val != "" {
			vals[attr] = val
		}
	}

	user := upspin.UserName(vals[username])

	packer := pack.LookupByName(vals[packing])
	if packer == nil {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("unknown packing %q", vals[packing]))
	}
	pack := packer.Packing()

	ctx := New()
	ctx = SetUserName(ctx, user)
	ctx = SetPacking(ctx, pack)

	var err error
	dir := vals[secrets]
	if dir == "" {
		dir, err = sshdir()
		if err != nil {
			return nil, errors.E(op, errors.Errorf("cannot find .ssh directory: %v", err))
		}
	}
	if dir == "none" {
		err = ErrNoFactotum
	} else {
		f, err := factotum.NewFromDir(dir)
		if err != nil {
			return nil, errors.E(op, err)
		}
		ctx = SetFactotum(ctx, f)
		// This must be done before bind so that keys are ready for authenticating to servers.
	}

	ctx = SetKeyEndpoint(ctx, parseEndpoint(op, vals, keyserver, &err))
	ctx = SetStoreEndpoint(ctx, parseEndpoint(op, vals, storeserver, &err))
	ctx = SetStoreCacheEndpoint(ctx, parseEndpoint(op, vals, storecache, &err))
	ctx = SetDirEndpoint(ctx, parseEndpoint(op, vals, dirserver, &err))
	return ctx, err
}

// ErrNoFactotum indicates that the returned context contains no Factotum, and
// that the user requested this by setting secrets=none in the configuration.
var ErrNoFactotum = errors.Str("Factotum not initialized: no secrets provided")

var ep0 upspin.Endpoint // Will have upspin.Unassigned as transport.

func parseEndpoint(op string, vals map[string]string, key string, errorp *error) upspin.Endpoint {
	text, ok := vals[key]
	if !ok || text == "" {
		return ep0
	}
	ep, err := upspin.ParseEndpoint(text)
	if err != nil {
		err = errors.E(op, errors.Errorf("cannot parse service %q: %v", text, err))
		log.Error.Print(err)
		if *errorp == nil {
			*errorp = err
		}
		return ep0
	}
	return *ep
}

type ctxUserName struct {
	upspin.Context
	userName upspin.UserName
}

func (ctx ctxUserName) UserName() upspin.UserName {
	return ctx.userName
}

// SetUserName returns a context derived from the given context
// with the given user name.
func SetUserName(ctx upspin.Context, u upspin.UserName) upspin.Context {
	return ctxUserName{
		Context:  ctx,
		userName: u,
	}
}

type ctxFactotum struct {
	upspin.Context
	factotum upspin.Factotum
}

func (ctx ctxFactotum) Factotum() upspin.Factotum {
	return ctx.factotum
}

// SetFactotum returns a context derived from the given context
// with the given factotum.
func SetFactotum(ctx upspin.Context, f upspin.Factotum) upspin.Context {
	return ctxFactotum{
		Context:  ctx,
		factotum: f,
	}
}

type ctxPacking struct {
	upspin.Context
	packing upspin.Packing
}

func (ctx ctxPacking) Packing() upspin.Packing {
	return ctx.packing
}

// SetPacking returns a context derived from the given context
// with the given factotum.
func SetPacking(ctx upspin.Context, p upspin.Packing) upspin.Context {
	return ctxPacking{
		Context: ctx,
		packing: p,
	}
}

type ctxKeyEndpoint struct {
	upspin.Context
	keyEndpoint upspin.Endpoint
}

func (ctx ctxKeyEndpoint) KeyEndpoint() upspin.Endpoint {
	return ctx.keyEndpoint
}

// SetKeyEndpoint returns a context derived from the given context
// with the given key endpoint.
func SetKeyEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return ctxKeyEndpoint{
		Context:     ctx,
		keyEndpoint: e,
	}
}

type ctxStoreEndpoint struct {
	upspin.Context
	storeEndpoint upspin.Endpoint
}

func (ctx ctxStoreEndpoint) StoreEndpoint() upspin.Endpoint {
	return ctx.storeEndpoint
}

// SetStoreEndpoint returns a context derived from the given context
// with the given store endpoint.
func SetStoreEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return ctxStoreEndpoint{
		Context:       ctx,
		storeEndpoint: e,
	}
}

type ctxStoreCacheEndpoint struct {
	upspin.Context
	storeCacheEndpoint upspin.Endpoint
}

func (ctx ctxStoreCacheEndpoint) StoreCacheEndpoint() upspin.Endpoint {
	return ctx.storeCacheEndpoint
}

// SetStoreCacheEndpoint returns a context derived from the given context
// with the given store cache endpoint.
func SetStoreCacheEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return ctxStoreCacheEndpoint{
		Context:            ctx,
		storeCacheEndpoint: e,
	}
}

type ctxDirEndpoint struct {
	upspin.Context
	dirEndpoint upspin.Endpoint
}

func (ctx ctxDirEndpoint) DirEndpoint() upspin.Endpoint {
	return ctx.dirEndpoint
}

// SetDirEndpoint returns a context derived from the given context
// with the given dir endpoint.
func SetDirEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return ctxDirEndpoint{
		Context:     ctx,
		dirEndpoint: e,
	}
}

func homedir() (string, error) {
	u, err := user.Current()
	// user.Current may return an error, but we should only handle it if it
	// returns a nil user. This is because os/user is wonky without cgo,
	// but it should work well enough for our purposes.
	if u == nil {
		e := errors.Str("lookup of current user failed")
		if err != nil {
			e = errors.Errorf("%v: %v", e, err)
		}
		return "", e
	}
	h := u.HomeDir
	if h == "" {
		return "", errors.E(errors.NotExist, errors.Str("user home directory not found"))
	}
	if err := isDir(h); err != nil {
		return "", err
	}
	return h, nil
}

func sshdir() (string, error) {
	h, err := homedir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(h, ".ssh")
	if err := isDir(p); err != nil {
		return "", err
	}
	return p, nil
}

func isDir(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return errors.E(errors.IO, err)
	}
	if !fi.IsDir() {
		return errors.E(errors.NotDir, errors.Str(p))
	}
	return nil
}
