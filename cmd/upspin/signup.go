// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
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
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) signup(args ...string) {
	const help = `
Signup generates an Upspin configuration file and private/public key pair and
stores them locally and sends a signup request to the public Upspin key server
at key.upspin.io. The server will respond by sending a confirmation email to
the given email address (or "username").

Signup writes a the configuration file to $HOME/upspin/config, holding the
username and the location of the directory and store servers. It writes the
public and private keys to $HOME/.ssh. These locations may be set using the
-config and -where flags.

The -dir and -store flags specify the network addresses of the Store and
Directory servers that the Upspin user will use. The -server flag may be used
to specify a single server that acts as both Store and Directory, in which case
the -dir and -store flags must not be set.

By default, signup creates new keys with the p256 cryptographic curve set.
The -curve and -secretseed flags allow the user to control the curve or to
recreate or reuse prior keys.

The -signup flag tell signup to skip the generation of the configuration file
and keys and only send the signup request to the key server.
`
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	var (
		force       = fs.Bool("force", false, "create a new user even if keys and config file exist")
		configFile  = fs.String("config", "upspin/config", "location of the config `file`")
		where       = fs.String("where", filepath.Join(os.Getenv("HOME"), ".ssh"), "`directory` to store keys")
		dirServer   = fs.String("dir", "", "Directory server `address`")
		storeServer = fs.String("store", "", "Store server `address`")
		bothServer  = fs.String("server", "", "Store and Directory server `address` (if combined)")
		signup      = fs.Bool("signup", false, "Only send signup request to key server; do not generate config or keys")
	)
	// Used only in keygen.
	fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	fs.String("secretseed", "", "128 bit secret `seed` in proquint format")

	s.parseFlags(fs, args, help, "signup [flags] <username>")

	// Determine config file location.
	if !filepath.IsAbs(*configFile) {
		// User must have a home dir in the local OS.
		homedir, err := config.Homedir()
		if err != nil {
			s.exit(err)
		}
		*configFile = filepath.Join(homedir, *configFile)
	}

	if *signup {
		// Don't generate; just send the signup request to the key server.
		s.registerUser(*configFile)
		return
	}

	// Check flags.
	if fs.NArg() != 1 {
		fs.Usage()
	}
	if *bothServer != "" {
		if *dirServer != "" || *storeServer != "" {
			s.failf("if -server provided -dir and -store must not be set")
			fs.Usage()
		}
		*dirServer = *bothServer
		*storeServer = *bothServer
	}
	if *dirServer == "" || *storeServer == "" {
		s.failf("-dir and -store must both be provided")
		fs.Usage()
	}

	// Parse -dir and -store flags as addresses and construct remote endpoints.
	dirEndpoint, err := parseAddress(*dirServer)
	if err != nil {
		s.exitf("error parsing -dir=%q: %v", dirServer, err)
	}
	storeEndpoint, err := parseAddress(*storeServer)
	if err != nil {
		s.exitf("error parsing -store=%q: %v", storeServer, err)
	}

	// Parse user name.
	uname, _, domain, err := user.Parse(upspin.UserName(fs.Arg(0)))
	if err != nil {
		s.exitf("invalid user name %q: %v", fs.Arg(0), err)
	}
	userName := upspin.UserName(uname + "@" + domain)

	env := os.Environ()
	wipeUpspinEnvironment()
	defer restoreEnvironment(env)

	// Verify if we have a config file.
	_, err = config.FromFile(*configFile)
	if err == nil && !*force {
		s.exitf("%s already exists", *configFile)
	}

	// Write the config file.
	var configContents bytes.Buffer
	err = configTemplate.Execute(&configContents, configData{
		UserName:  userName,
		Dir:       dirEndpoint,
		Store:     storeEndpoint,
		SecretDir: *where,
		Packing:   "ee",
	})
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(*configFile, configContents.Bytes(), 0640)
	if err != nil {
		// Directory doesn't exist, perhaps.
		if !os.IsNotExist(err) {
			s.exitf("cannot create %s: %v", *configFile, err)
		}
		dir := filepath.Dir(*configFile)
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			// Looks like the directory exists, so stop now and report original error.
			s.exitf("cannot create %s: %v", *configFile, err)
		}
		if mkdirErr := os.Mkdir(dir, 0700); mkdirErr != nil {
			s.exitf("cannot make directory %s: %v", dir, mkdirErr)
		}
		err = ioutil.WriteFile(*configFile, configContents.Bytes(), 0640)
		if err != nil {
			s.exit(err)
		}
	}
	fmt.Println("Configuration file written to:")
	fmt.Printf("\t%s\n\n", *configFile)

	// Generate a new key.
	s.keygenCommand(fs)

	// Send the signup request to the key server.
	s.registerUser(*configFile)
}

func (s *State) registerUser(configFile string) {
	cfg, err := config.FromFile(configFile)
	if err != nil {
		s.exit(err)
	}

	// Make signup request.
	vals := url.Values{
		"name":  {string(cfg.UserName())},
		"dir":   {string(cfg.DirEndpoint().NetAddr)},
		"store": {string(cfg.StoreEndpoint().NetAddr)},
		"key":   {string(cfg.Factotum().PublicKey())},
	}
	signupURL := (&url.URL{
		Scheme:   "https",
		Host:     "key.upspin.io",
		Path:     "/signup",
		RawQuery: vals.Encode(),
	}).String()

	r, err := http.Post(signupURL, "text/plain", nil)
	if err != nil {
		s.exit(err)
	}
	b, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		s.exit(err)
	}
	if r.StatusCode != http.StatusOK {
		s.exitf("key server error: %s", b)
	}
	fmt.Printf("A signup email has been sent to %q,\n", cfg.UserName())
	fmt.Println("please read it for further instructions.")
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
