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

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

var inTest = false // Generate errors instead of logs for certain problems.

// contextImpl implements upspin.Context.
// The fields are pointers so that a nil indicates the field is unset.
// If a field is unset and base is non-nil, an access of that field will
// be delegated to the underlying base context.
type contextImpl struct {
	userName           *upspin.UserName
	packing            *upspin.Packing
	keyEndpoint        *upspin.Endpoint
	dirEndpoint        *upspin.Endpoint
	storeEndpoint      *upspin.Endpoint
	storeCacheEndpoint *upspin.Endpoint
	factotum           upspin.Factotum

	base upspin.Context
}

// New returns a context with all fields set as defaults.
func New() upspin.Context {
	return &contextImpl{}
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

	ctx := &contextImpl{
		userName: &user,
		packing:  &pack,
	}

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
		f, err := factotum.New(dir)
		if err != nil {
			return nil, errors.E(op, err)
		}
		ctx.factotum = f
		// This must be done before bind so that keys are ready for authenticating to servers.
	}

	ctx.keyEndpoint = parseEndpoint(op, vals, keyserver, &err)
	ctx.storeEndpoint = parseEndpoint(op, vals, storeserver, &err)
	ctx.storeCacheEndpoint = parseEndpoint(op, vals, storecache, &err)
	ctx.dirEndpoint = parseEndpoint(op, vals, dirserver, &err)
	return ctx, err
}

// ErrNoFactotum indicates that the returned context contains no Factotum, and
// that the user requested this by setting secrets=none in the configuration.
var ErrNoFactotum = errors.Str("Factotum not initialized: no secrets provided")

var ep0 upspin.Endpoint // Will have upspin.Unassigned as transport.

func parseEndpoint(op string, vals map[string]string, key string, errorp *error) *upspin.Endpoint {
	text, ok := vals[key]
	if !ok || text == "" {
		return nil
	}
	ep, err := upspin.ParseEndpoint(text)
	if err != nil {
		err = errors.E(op, errors.Errorf("cannot parse service %q: %v", text, err))
		log.Error.Print(err)
		if *errorp == nil {
			*errorp = err
		}
		return nil
	}
	return ep
}

// KeyServer implements upspin.Context.
func (ctx *contextImpl) KeyServer() upspin.KeyServer {
	u, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		u, _ = bind.KeyServer(ctx, ep0)
	}
	return u
}

// DirServer implements upspin.Context.
func (ctx *contextImpl) DirServer(name upspin.PathName) upspin.DirServer {
	if len(name) == 0 {
		// If name is empty, just return the directory at ctx.DirEndpoint().
		d, err := bind.DirServer(ctx, ctx.DirEndpoint())
		if err != nil {
			d, _ = bind.DirServer(ctx, ep0)
		}
		return d
	}
	parsed, err := path.Parse(name)
	if err != nil {
		d, _ := bind.DirServer(ctx, ep0)
		return d
	}
	var endpoints []upspin.Endpoint
	if parsed.User() == ctx.UserName() {
		endpoints = append(endpoints, ctx.DirEndpoint())
	}
	if u, err := ctx.KeyServer().Lookup(parsed.User()); err == nil {
		endpoints = append(endpoints, u.Dirs...)
	}
	for _, e := range endpoints {
		d, _ := bind.DirServer(ctx, e)
		if d != nil {
			return d
		}
	}
	d, _ := bind.DirServer(ctx, ep0)
	return d
}

// StoreServer implements upspin.Context.
func (ctx *contextImpl) StoreServer() upspin.StoreServer {
	u, err := bind.StoreServer(ctx, ctx.StoreEndpoint())
	if err != nil {
		u, _ = bind.StoreServer(ctx, ep0)
	}
	return u
}

// StoreServerFor implements upspin.Context.
func (ctx *contextImpl) StoreServerFor(ep upspin.Endpoint) (upspin.StoreServer, error) {
	return bind.StoreServer(ctx, ep)
}

// UserName implements upspin.Context.
func (ctx *contextImpl) UserName() upspin.UserName {
	if ctx.userName != nil {
		return *ctx.userName
	}
	if ctx.base != nil {
		return ctx.base.UserName()
	}
	return defaultUserName
}

// SetUserName returns a context derived from the given context
// with the given user name.
func SetUserName(ctx upspin.Context, u upspin.UserName) upspin.Context {
	return &contextImpl{
		base:     ctx,
		userName: &u,
	}
}

// Factotum implements upspin.Context.
func (ctx *contextImpl) Factotum() upspin.Factotum {
	if ctx.factotum != nil {
		return ctx.factotum
	}
	if ctx.base != nil {
		return ctx.base.Factotum()
	}
	return nil
}

// SetFactotum returns a context derived from the given context
// with the given factotum.
func SetFactotum(ctx upspin.Context, f upspin.Factotum) upspin.Context {
	return &contextImpl{
		base:     ctx,
		factotum: f,
	}
}

// Packing implements upspin.Context.
func (ctx *contextImpl) Packing() upspin.Packing {
	if ctx.packing != nil {
		return *ctx.packing
	}
	if ctx.base != nil {
		return ctx.base.Packing()
	}
	return defaultPacking
}

// SetPacking returns a context derived from the given context
// with the given factotum.
func SetPacking(ctx upspin.Context, p upspin.Packing) upspin.Context {
	return &contextImpl{
		base:    ctx,
		packing: &p,
	}
}

// KeyEndpoint implements upspin.Context.
func (ctx *contextImpl) KeyEndpoint() upspin.Endpoint {
	if ctx.keyEndpoint != nil {
		return *ctx.keyEndpoint
	}
	if ctx.base != nil {
		return ctx.base.KeyEndpoint()
	}
	return ep0
}

// SetKeyEndpoint returns a context derived from the given context
// with the given key endpoint.
func SetKeyEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return &contextImpl{
		base:        ctx,
		keyEndpoint: &e,
	}
}

// DirEndpoint implements upspin.Context.
func (ctx *contextImpl) DirEndpoint() upspin.Endpoint {
	if ctx.dirEndpoint != nil {
		return *ctx.dirEndpoint
	}
	if ctx.base != nil {
		return ctx.base.DirEndpoint()
	}
	return ep0
}

// SetDirEndpoint returns a context derived from the given context
// with the given dir endpoint.
func SetDirEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return &contextImpl{
		base:        ctx,
		dirEndpoint: &e,
	}
}

// StoreEndpoint implements upspin.Context.
func (ctx *contextImpl) StoreEndpoint() upspin.Endpoint {
	if ctx.storeEndpoint != nil {
		return *ctx.storeEndpoint
	}
	if ctx.base != nil {
		return ctx.base.StoreEndpoint()
	}
	return ep0
}

// SetStoreEndpoint returns a context derived from the given context
// with the given store endpoint.
func SetStoreEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return &contextImpl{
		base:          ctx,
		storeEndpoint: &e,
	}
}

// StoreCacheEndpoint implements upspin.Context.
func (ctx *contextImpl) StoreCacheEndpoint() upspin.Endpoint {
	if ctx.storeCacheEndpoint != nil {
		return *ctx.storeCacheEndpoint
	}
	if ctx.base != nil {
		return ctx.base.StoreCacheEndpoint()
	}
	return ep0
}

// SetStoreCacheEndpoint returns a context derived from the given context
// with the given store cache endpoint.
func SetStoreCacheEndpoint(ctx upspin.Context, e upspin.Endpoint) upspin.Context {
	return &contextImpl{
		base:               ctx,
		storeCacheEndpoint: &e,
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
