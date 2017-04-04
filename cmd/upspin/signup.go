// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/subcmd"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) signup(args ...string) {
	const help = `
Signup generates an Upspin configuration file and private/public key pair,
stores them locally, and sends a signup request to the public Upspin key server
at key.upspin.io. The server will respond by sending a confirmation email to
the given email address (or "username").

The email address becomes a username after successful signup but is never
again used by Upspin to send or receive email. Therefore the email address
may be disabled once signup is complete if one wishes to have an Upspin
name distinct from one's regular email address. Either way, if the email
address is compromised after Upspin signup, the security of the user's
Upspin data is unaffected.

Signup writes a configuration file to $HOME/upspin/config, holding the
username and the location of the directory and store servers. It writes the
public and private keys to $HOME/.ssh. These locations may be set using the
global -config and signup-specific -where flags.

The -dir and -store flags specify the network addresses of the Store and
Directory servers that the Upspin user will use. The -server flag may be used
to specify a single server that acts as both Store and Directory, in which case
the -dir and -store flags must not be set.

By default, signup creates new keys with the p256 cryptographic curve set.
The -curve and -secretseed flags allow the user to control the curve or to
recreate or reuse prior keys.

The -signuponly flag tells signup to skip the generation of the configuration
file and keys and only send the signup request to the key server.
`
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	var (
		force       = fs.Bool("force", false, "create a new user even if keys and config file exist")
		where       = fs.String("where", filepath.Join(config.Home(), ".ssh"), "`directory` to store keys")
		dirServer   = fs.String("dir", "", "Directory server `address`")
		storeServer = fs.String("store", "", "Store server `address`")
		bothServer  = fs.String("server", "", "Store and Directory server `address` (if combined)")
		signupOnly  = fs.Bool("signuponly", false, "only send signup request to key server; do not generate config or keys")
	)
	// Used only in keygen.
	fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	fs.String("secretseed", "", "the seed containing a 128 bit secret in proquint format or a file that contains it")
	fs.Bool("rotate", false, "always false during sign up")

	s.ParseFlags(fs, args, help, "[-config=<file>] signup -dir=<addr> -store=<addr> [flags] <username>\n       upspin [-config=<file>] signup -server=<addr> [flags] <username>")

	// Determine config file location.
	if !filepath.IsAbs(flags.Config) {
		// User must have a home dir in the local OS.
		homedir, err := config.Homedir()
		if err != nil {
			s.Exit(err)
		}
		flags.Config = filepath.Join(homedir, flags.Config)
	}

	if *signupOnly {
		// Don't generate; just send the signup request to the key server.
		s.registerUser(flags.Config)
		return
	}

	// Check flags.
	if fs.NArg() != 1 {
		fs.Usage()
	}
	if *bothServer != "" {
		if *dirServer != "" || *storeServer != "" {
			s.Failf("if -server provided -dir and -store must not be set")
			fs.Usage()
		}
		*dirServer = *bothServer
		*storeServer = *bothServer
	}
	if *dirServer == "" || *storeServer == "" {
		s.Failf("-dir and -store must both be provided")
		fs.Usage()
	}

	// Parse -dir and -store flags as addresses and construct remote endpoints.
	dirEndpoint, err := parseAddress(*dirServer)
	if err != nil {
		s.Exitf("error parsing -dir=%q: %v", dirServer, err)
	}
	storeEndpoint, err := parseAddress(*storeServer)
	if err != nil {
		s.Exitf("error parsing -store=%q: %v", storeServer, err)
	}

	// Parse user name.
	uname, suffix, domain, err := user.Parse(upspin.UserName(fs.Arg(0)))
	if err != nil {
		s.Exitf("invalid user name %q: %v", fs.Arg(0), err)
	}
	if suffix != "" {
		s.Exitf("invalid user name %q: name must not include a +suffix; for a suffixed user, use upspin user -put", fs.Arg(0))
	}

	userName := upspin.UserName(uname + "@" + domain)

	env := os.Environ()
	wipeUpspinEnvironment()
	defer restoreEnvironment(env)

	// Verify if we have a config file.
	_, err = config.FromFile(flags.Config)
	if err == nil && !*force {
		s.Exitf("%s already exists", flags.Config)
	}

	// Write the config file.
	var configContents bytes.Buffer
	err = configTemplate.Execute(&configContents, configData{
		UserName:  userName,
		Dir:       dirEndpoint,
		Store:     storeEndpoint,
		SecretDir: subcmd.Tilde(*where),
		Packing:   "ee",
	})
	if err != nil {
		s.Exit(err)
	}
	err = ioutil.WriteFile(flags.Config, configContents.Bytes(), 0640)
	if err != nil {
		// Directory doesn't exist, perhaps.
		if !os.IsNotExist(err) {
			s.Exitf("cannot create %s: %v", flags.Config, err)
		}
		dir := filepath.Dir(flags.Config)
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			// Looks like the directory exists, so stop now and report original error.
			s.Exitf("cannot create %s: %v", flags.Config, err)
		}
		if mkdirErr := os.Mkdir(dir, 0700); mkdirErr != nil {
			s.Exitf("cannot make directory %s: %v", dir, mkdirErr)
		}
		err = ioutil.WriteFile(flags.Config, configContents.Bytes(), 0640)
		if err != nil {
			s.Exit(err)
		}
	}
	fmt.Fprintf(os.Stderr, "Configuration file written to:\n")
	fmt.Fprintf(os.Stderr, "\t%s\n\n", flags.Config)

	// Generate a new key.
	s.keygenCommand(fs)

	// Send the signup request to the key server.
	s.registerUser(flags.Config)
}

// registerUser reads the config file and sends its information to the key server.
func (s *State) registerUser(configFile string) {
	cfg, err := config.FromFile(configFile)
	if err != nil {
		s.Exit(err)
	}

	// Make signup request.
	signupURL, err := makeSignupURL(cfg)
	if err != nil {
		s.Exit(err)
	}
	r, err := http.Post(signupURL, "text/plain", nil)
	if err != nil {
		s.Exit(err)
	}
	b, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		s.Exit(err)
	}
	if r.StatusCode != http.StatusOK {
		s.Exitf("key server error: %s", b)
	}
	fmt.Fprintf(os.Stderr, "A signup email has been sent to %q,\n", cfg.UserName())
	fmt.Fprintf(os.Stderr, "please read it for further instructions.\n")
}

// makeSignupURL returns an encoded URL used to sign up a new user with the
// default keyserver.
func makeSignupURL(cfg upspin.Config) (string, error) {
	hash, vals := signupRequestHash(cfg.UserName(), cfg.DirEndpoint().NetAddr, cfg.StoreEndpoint().NetAddr, cfg.Factotum().PublicKey())
	sig, err := cfg.Factotum().Sign(hash)
	if err != nil {
		return "", err
	}
	vals.Add("sigR", sig.R.String())
	vals.Add("sigS", sig.S.String())
	return (&url.URL{
		Scheme:   "https",
		Host:     "key.upspin.io",
		Path:     "/signup",
		RawQuery: vals.Encode(),
	}).String(), nil
}

type configData struct {
	UserName   upspin.UserName
	Store, Dir *upspin.Endpoint
	SecretDir  string
	Packing    string
}

var configTemplate = template.Must(template.New("config").Parse(`
username: {{.UserName}}
secrets: {{.SecretDir}}
storeserver: {{.Store}}
dirserver: {{.Dir}}
packing: {{.Packing}}
`))

func parseAddress(a string) (*upspin.Endpoint, error) {
	host, port, err := net.SplitHostPort(a)
	if err != nil {
		var err2 error
		host, port, err2 = net.SplitHostPort(a + ":443")
		if err2 != nil {
			return nil, err
		}
	}
	return upspin.ParseEndpoint(fmt.Sprintf("remote,%s:%s", host, port))
}

func wipeUpspinEnvironment() {
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "upspin") {
			os.Setenv(env, "")
		}
	}
}

func restoreEnvironment(env []string) {
	for _, e := range env {
		kv := strings.Split(e, "=")
		if len(kv) != 2 {
			continue
		}
		os.Setenv(kv[0], kv[1])
	}
}

// signupRequestHash generates a hash of the supplied arguments
// that, when signed, is used to prove that a signup request originated
// from the user that owns the supplied private key.
// Keep it in sync with cmd/keyserver/signup.go.
func signupRequestHash(name upspin.UserName, dir, store upspin.NetAddr, key upspin.PublicKey) ([]byte, url.Values) {
	const magic = "signup-request"

	u := url.Values{}
	h := sha256.New()
	h.Write([]byte(magic))
	w := func(key, val string) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(val)))
		h.Write(l[:])
		h.Write([]byte(val))
		u.Add(key, val)
	}
	w("name", string(name))
	w("dir", string(dir))
	w("store", string(store))
	w("key", string(key))
	return h.Sum(nil), u
}
