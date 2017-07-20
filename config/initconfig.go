// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package config creates a client configuration from various sources.
package config // import "upspin.io/config"

import (
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	osuser "os/user"
	"path/filepath"
	"strings"

	yaml "gopkg.in/yaml.v2"

	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/rpc/local"
	"upspin.io/upspin"
	"upspin.io/user"

	// Needed because the default packing is "ee" and its
	// implementation is referenced if no packing is specified.
	_ "upspin.io/pack/ee"
)

var inTest = false // Generate errors instead of logs for certain problems.

// base implements upspin.Config, returning default values for all operations.
type base struct{}

func (base) UserName() upspin.UserName      { return defaultUserName }
func (base) Factotum() upspin.Factotum      { return nil }
func (base) Packing() upspin.Packing        { return defaultPacking }
func (base) KeyEndpoint() upspin.Endpoint   { return defaultKeyEndpoint }
func (base) DirEndpoint() upspin.Endpoint   { return upspin.Endpoint{} }
func (base) StoreEndpoint() upspin.Endpoint { return upspin.Endpoint{} }
func (base) CacheEndpoint() upspin.Endpoint { return upspin.Endpoint{} }
func (base) CertPool() *x509.CertPool       { return nil }
func (base) Flags(string) map[string]string { return nil }

// New returns a config with all fields set as defaults.
func New() upspin.Config {
	return base{}
}

var (
	defaultUserName    = upspin.UserName("noone@nowhere.org")
	defaultPacking     = upspin.EEPack
	defaultKeyEndpoint = upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   "key.upspin.io:443",
	}
)

// Known keys. All others are treated as errors.
const (
	username    = "username"
	keyserver   = "keyserver"
	dirserver   = "dirserver"
	storeserver = "storeserver"
	cache       = "cache"
	packing     = "packing"
	secrets     = "secrets"
	tlscerts    = "tlscerts"
)

// ErrNoFactotum indicates that the returned config contains no Factotum, and
// that the user requested this by setting secrets=none in the configuration.
var ErrNoFactotum = errors.Str("factotum not initialized: no secrets provided")

// FromFile initializes a config using the given file. If the file cannot
// be opened but the name can be found in $HOME/upspin, that file is used.
func FromFile(name string) (upspin.Config, error) {
	f, err := os.Open(name)
	if err != nil && !filepath.IsAbs(name) && os.IsNotExist(err) {
		// It's a local name, so, try adding $HOME/upspin
		home, errHome := Homedir()
		if errHome == nil {
			f, err = os.Open(filepath.Join(home, "upspin", name))
		}
	}
	if err != nil {
		const op = "config.FromFile"
		if os.IsNotExist(err) {
			return nil, errors.E(op, errors.NotExist, err)
		}
		return nil, errors.E(op, err)
	}
	defer f.Close()
	return InitConfig(f)
}

// InitConfig returns a config generated from a configuration file and/or
// environment variables.
//
// A configuration file should be of the format
//   # lines that begin with a hash are ignored
//   key = value
// where key may be one of username, keyserver, dirserver, storeserver,
// packing, secrets, or tlscerts.
//
// The default configuration file location is $HOME/upspin/config.
// If passed a non-nil io.Reader, that is used instead of the default file.
//
// Environment variables named "upspinkey", where "key" is a recognized
// configuration key, may override configuration values in the config file.
//
// Any endpoints (keyserver, dirserver, storeserver) not set in the data for
// the config will be set to the "unassigned" transport and an empty network
// address, except keyserver which defaults to "remote,key.upspin.io:443".
// If an endpoint is specified without a transport it is assumed to be
// the address component of a remote endpoint.
// If a remote endpoint is specified without a port in its address component
// the port is assumed to be 443.
//
// The default value for packing is "ee".
//
// The default value for secrets is "$HOME/.ssh".
// The special value "none" indicates there are no secrets to load;
// in this case, the returned config will not include a Factotum
// and the returned error is ErrNoFactotum.
//
// The tlscerts key specifies a directory containing PEM certificates define
// the certificate pool used for verifying client TLS connections,
// replacing the root certificate list provided by the operating system.
// Files without the suffix ".pem" are ignored.
// The default value for tlscerts is the empty string,
// in which case just the system roots are used.
func InitConfig(r io.Reader) (upspin.Config, error) {
	const op = "config.InitConfig"
	vals := map[string]string{
		username:    string(defaultUserName),
		packing:     defaultPacking.String(),
		keyserver:   defaultKeyEndpoint.String(),
		dirserver:   "",
		storeserver: "",
		cache:       "no",
		secrets:     "",
		tlscerts:    "",
	}
	cmdFlagVals := make(map[string]map[string]string)

	// If the provided reader is nil, try $HOME/upspin/config.
	if r == nil {
		home, err := Homedir()
		if err != nil {
			return nil, errors.E(op, err)
		}
		f, err := os.Open(filepath.Join(home, "upspin/config"))
		if err != nil {
			return nil, errors.E(op, err)
		}
		r = f
		defer f.Close()
	}

	// Read the YAML definition.
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if err := valsFromYAML(vals, cmdFlagVals, data); err != nil {
		return nil, errors.E(op, err)
	}

	// Construct a config from vals.
	cfg := New()

	// Put the canonical respresentation of the username in the config.
	username, err := user.Clean(upspin.UserName(vals[username]))
	if err != nil {
		return nil, errors.E(op, err)
	}
	cfg = SetUserName(cfg, username)

	packer := pack.LookupByName(vals[packing])
	if packer == nil {
		return nil, errors.E(op, errors.Invalid, errors.Errorf("unknown packing %q", vals[packing]))
	}
	cfg = SetPacking(cfg, packer.Packing())

	if dir := vals[tlscerts]; dir != "" {
		pool, err := certPoolFromDir(dir)
		if err != nil {
			return nil, errors.E(op, err)
		}
		if pool != nil {
			cfg = SetCertPool(cfg, pool)
		} else {
			log.Info.Printf("config: no PEM certificates found in %q", dir)
		}
	}

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
		cfg = SetFactotum(cfg, f)
		// This must be done before bind so that keys are ready for authenticating to servers.
	}

	if len(cmdFlagVals) != 0 {
		cfg = SetFlags(cfg, cmdFlagVals)
	}

	cfg = SetKeyEndpoint(cfg, parseEndpoint(op, vals, keyserver, &err))
	cfg = SetStoreEndpoint(cfg, parseEndpoint(op, vals, storeserver, &err))
	cfg = SetDirEndpoint(cfg, parseEndpoint(op, vals, dirserver, &err))

	// A shorthand for the default local address.
	// TODO(p): phase out the ability to specify an address, yes or no should suffice.
	switch vals[cache] {
	case "y", "yes", "true":
		vals[cache] = local.LocalName(cfg, "cacheserver")
	case "n", "no", "false":
		vals[cache] = ""
	}
	cfg = SetCacheEndpoint(cfg, parseEndpoint(op, vals, cache, &err))

	return cfg, err
}

// valsFromYAML parses YAML from the given map and puts the values
// into the provided map. Unrecognized keys generate an error.
func valsFromYAML(vals map[string]string, cmdFlagVals map[string]map[string]string, data []byte) error {
	newVals := map[string]interface{}{}
	if err := yaml.Unmarshal(data, newVals); err != nil {
		return errors.E(errors.Invalid, errors.Errorf("parsing YAML file: %v", err))
	}
	for k, v := range newVals {
		if k == "cmdflags" {
			if err := asFlags(v, cmdFlagVals); err != nil {
				return err
			}
			continue
		}
		if _, ok := vals[k]; !ok {
			return errors.E(errors.Invalid, errors.Errorf("unrecognized key %q", k))
		}
		if s, err := asString(v); err != nil {
			return fmt.Errorf("%q: %v", k, err)
		} else {
			vals[k] = s
		}
	}
	return nil
}

// asString tries to convert a value back into its original string. This will not
// always be possible but should be for all our expected use cases.
func asString(v interface{}) (string, error) {
	switch vc := v.(type) {
	case int, int32, int64, uint, uint32, uint64, float32, float64, bool:
		return fmt.Sprintf("%v", vc), nil
	case string:
		return vc, nil
	}
	return "", errors.E(errors.Invalid, errors.Errorf("unrecognized value %T", v))
}

func asFlags(v interface{}, m map[string]map[string]string) error {
	cmds, ok := v.(map[interface{}]interface{})
	if !ok {
		return errors.E(errors.Invalid, errors.Errorf("unrecognized cmdflags %v", v))
	}
	for k, v := range cmds {
		cmd, err := asString(k)
		if err != nil {
			return errors.E(errors.Invalid, errors.Errorf("bad command %q %v", v, err))
		}
		flags, ok := v.(map[interface{}]interface{})
		if !ok {
			return errors.E(errors.Invalid, errors.Errorf("cmd %q has bad value: %v", cmd, v))
		}
		fm := make(map[string]string)
		for k, v := range flags {
			flag, err := asString(k)
			if err != nil {
				return errors.E(errors.Invalid, errors.Errorf("cmd %q has bad flag: %s", cmd, err))
			}
			val, err := asString(v)
			if err != nil {
				return errors.E(errors.Invalid, errors.Errorf("cmd %q flag %q has bad value: %s", cmd, flag, err))
			}
			fm[flag] = val
		}
		m[cmd] = fm
	}
	return nil
}

// certPoolFromDir parses any PEM files in the provided directory
// and returns the resulting pool.
func certPoolFromDir(dir string) (*x509.CertPool, error) {
	var pool *x509.CertPool
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, errors.Errorf("reading TLS Certificates in %q: %v", dir, err)
	}
	for _, fi := range fis {
		name := fi.Name()
		if filepath.Ext(name) != ".pem" {
			continue
		}
		pem, err := ioutil.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, errors.Errorf("reading TLS Certificate %q: %v", name, err)
		}
		if pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pem)
	}
	return pool, nil
}

func parseEndpoint(op string, vals map[string]string, key string, errorp *error) upspin.Endpoint {
	text, ok := vals[key]
	if !ok || text == "" {
		return upspin.Endpoint{}
	}

	ep, err := upspin.ParseEndpoint(text)
	// If no transport is provided, assume remote transport.
	if err != nil && !strings.Contains(text, ",") {
		if ep2, err2 := upspin.ParseEndpoint("remote," + text); err2 == nil {
			ep = ep2
			err = nil
		}
	}
	if err != nil {
		err = errors.E(op, errors.Errorf("cannot parse service %q: %v", text, err))
		log.Error.Print(err)
		if *errorp == nil {
			*errorp = err
		}
		return upspin.Endpoint{}
	}

	// If it's a remote and the provided address does not include a port,
	// assume port 443.
	if ep.Transport == upspin.Remote && !strings.Contains(string(ep.NetAddr), ":") {
		ep.NetAddr += ":443"
	}

	return *ep
}

type cfgUserName struct {
	upspin.Config
	userName upspin.UserName
}

func (cfg cfgUserName) UserName() upspin.UserName {
	return cfg.userName
}

// SetUserName returns a config derived from the given config
// with the given user name.
func SetUserName(cfg upspin.Config, u upspin.UserName) upspin.Config {
	return cfgUserName{
		Config:   cfg,
		userName: u,
	}
}

type cfgFactotum struct {
	upspin.Config
	factotum upspin.Factotum
}

func (cfg cfgFactotum) Factotum() upspin.Factotum {
	return cfg.factotum
}

// SetFactotum returns a config derived from the given config
// with the given factotum.
func SetFactotum(cfg upspin.Config, f upspin.Factotum) upspin.Config {
	return cfgFactotum{
		Config:   cfg,
		factotum: f,
	}
}

type cfgPacking struct {
	upspin.Config
	packing upspin.Packing
}

func (cfg cfgPacking) Packing() upspin.Packing {
	return cfg.packing
}

// SetPacking returns a config derived from the given config
// with the given packing.
func SetPacking(cfg upspin.Config, p upspin.Packing) upspin.Config {
	return cfgPacking{
		Config:  cfg,
		packing: p,
	}
}

type cfgKeyEndpoint struct {
	upspin.Config
	keyEndpoint upspin.Endpoint
}

func (cfg cfgKeyEndpoint) KeyEndpoint() upspin.Endpoint {
	return cfg.keyEndpoint
}

// SetKeyEndpoint returns a config derived from the given config
// with the given key endpoint.
func SetKeyEndpoint(cfg upspin.Config, e upspin.Endpoint) upspin.Config {
	return cfgKeyEndpoint{
		Config:      cfg,
		keyEndpoint: e,
	}
}

type cfgStoreEndpoint struct {
	upspin.Config
	storeEndpoint upspin.Endpoint
}

func (cfg cfgStoreEndpoint) StoreEndpoint() upspin.Endpoint {
	return cfg.storeEndpoint
}

// SetStoreEndpoint returns a config derived from the given config
// with the given store endpoint.
func SetStoreEndpoint(cfg upspin.Config, e upspin.Endpoint) upspin.Config {
	return cfgStoreEndpoint{
		Config:        cfg,
		storeEndpoint: e,
	}
}

type cfgCacheEndpoint struct {
	upspin.Config
	cacheEndpoint upspin.Endpoint
}

func (cfg cfgCacheEndpoint) CacheEndpoint() upspin.Endpoint {
	return cfg.cacheEndpoint
}

// SetCacheEndpoint returns a config derived from the given config
// with the given cache endpoint.
func SetCacheEndpoint(cfg upspin.Config, e upspin.Endpoint) upspin.Config {
	return cfgCacheEndpoint{
		Config:        cfg,
		cacheEndpoint: e,
	}
}

type cfgDirEndpoint struct {
	upspin.Config
	dirEndpoint upspin.Endpoint
}

func (cfg cfgDirEndpoint) DirEndpoint() upspin.Endpoint {
	return cfg.dirEndpoint
}

// SetDirEndpoint returns a config derived from the given config
// with the given dir endpoint.
func SetDirEndpoint(cfg upspin.Config, e upspin.Endpoint) upspin.Config {
	return cfgDirEndpoint{
		Config:      cfg,
		dirEndpoint: e,
	}
}

type cfgCertPool struct {
	upspin.Config
	pool *x509.CertPool
}

func (cfg cfgCertPool) CertPool() *x509.CertPool {
	return cfg.pool
}

func SetCertPool(cfg upspin.Config, pool *x509.CertPool) upspin.Config {
	return cfgCertPool{
		Config: cfg,
		pool:   pool,
	}
}

type cfgFlags struct {
	upspin.Config
	flags map[string]map[string]string
}

func (cfg cfgFlags) Flags(cmd string) map[string]string {
	return cfg.flags[cmd]
}

func SetFlags(cfg upspin.Config, flags map[string]map[string]string) upspin.Config {
	return cfgFlags{
		Config: cfg,
		flags:  flags,
	}
}

// SetFlagValues updates any flag that is still at its default value. It will
// apply all the flags possible and return the last error seen.
func SetFlagValues(cfg upspin.Config, cmd string) error {
	const op = "config.SetFlagValues"
	flags := cfg.Flags(cmd)
	if flags == nil {
		return nil
	}
	var lasterr error
	for k, v := range flags {
		f := flag.Lookup(k)
		if f == nil {
			lasterr = errors.E(op, errors.Invalid, errors.Errorf("unknown flag %q", k))
			continue
		}
		if f.Value.String() != f.DefValue {
			continue
		}
		if err := flag.Set(k, v); err != nil {
			lasterr = errors.E(op, err)
			continue
		}
	}
	return lasterr
}

// TODO(adg): move to osutil package?
// Homedir returns the home directory of the OS' logged-in user.
func Homedir() (string, error) {
	u, err := osuser.Current()
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

// Home returns the home directory of the user, or panics if it cannot find one.
func Home() string {
	home, err := Homedir()
	if err != nil {
		panic(err)
	}
	return home
}

func sshdir() (string, error) {
	h, err := Homedir()
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
