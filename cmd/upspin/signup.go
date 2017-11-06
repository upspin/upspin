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
	"os"
	"path/filepath"
	"text/template"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/serverutil/signup"
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
	defaultKeyServer := string(config.New().KeyEndpoint().NetAddr)
	var (
		force       = fs.Bool("force", false, "create a new user even if keys and config file exist")
		keyServer   = fs.String("key", defaultKeyServer, "Key server `address`")
		dirServer   = fs.String("dir", "", "Directory server `address`")
		storeServer = fs.String("store", "", "Store server `address`")
		bothServer  = fs.String("server", "", "Store and Directory server `address` (if combined)")
		signupOnly  = fs.Bool("signuponly", false, "only send signup request to key server; do not generate config or keys")
		secrets     = fs.String("secrets", "", "`directory` to store key pair")
		curve       = fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
		secretseed  = fs.String("secretseed", "", "the seed containing a 128 bit secret in proquint format or a file that contains it")
	)

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
		s.registerUser(*keyServer)
		return
	}

	// Check flags.
	if fs.NArg() != 1 {
		s.Failf("after flags parsed, expected 1 argument but saw %d", fs.NArg())
		usageAndExit(fs)
	}
	if *bothServer != "" {
		if *dirServer != "" || *storeServer != "" {
			s.Failf("if -server provided -dir and -store must not be set")
			usageAndExit(fs)
		}
		*dirServer = *bothServer
		*storeServer = *bothServer
	}
	if *dirServer == "" || *storeServer == "" {
		s.Failf("-dir and -store must both be provided")
		usageAndExit(fs)
	}

	// Parse -key, -dir and -store flags as addresses and construct remote endpoints.
	keyEndpoint, err := parseAddress(*keyServer)
	if err != nil {
		s.Exitf("error parsing -key=%q: %v", keyServer, err)
	}
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
	*secrets = subcmd.Tilde(*secrets)

	// Verify if we have a config file.
	_, err = config.FromFile(flags.Config)
	if err == nil && !*force {
		s.Exitf("%s already exists", flags.Config)
	}

	// Write the config file.
	var configContents bytes.Buffer
	err = configTemplate.Execute(&configContents, configData{
		UserName:  userName,
		Key:       keyEndpoint,
		Dir:       dirEndpoint,
		Store:     storeEndpoint,
		Packing:   "ee",
		SecretDir: *secrets,
	})
	if err != nil {
		s.Exit(err)
	}
	err = ioutil.WriteFile(flags.Config, configContents.Bytes(), 0640)
	if err != nil {
		// Directory doesn't exist, perhaps.
		if !os.IsNotExist(err) {
			s.Exit(err)
		}
		dir := filepath.Dir(flags.Config)
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			// Looks like the directory exists, so stop now and report original error.
			s.Exit(err)
		}
		if mkdirErr := os.Mkdir(dir, 0700); mkdirErr != nil {
			s.Exit(err)
		}
		err = ioutil.WriteFile(flags.Config, configContents.Bytes(), 0640)
		if err != nil {
			s.Exit(err)
		}
	}
	fmt.Fprintf(s.Stderr, "Configuration file written to:\n")
	fmt.Fprintf(s.Stderr, "\t%s\n\n", flags.Config)

	// Generate a new key.
	if *secrets == "" {
		// Use the default secrets directory if none specified.
		*secrets, err = config.DefaultSecretsDir(userName)
		if err != nil {
			s.Exit(err)
		}
	}
	s.keygenCommand(*secrets, *curve, *secretseed, false)

	// Send the signup request to the key server.
	s.registerUser(*keyServer)
}

// registerUser reads the config file and sends its information to the key server.
func (s *State) registerUser(keyServer string) {
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		s.Exit(err)
	}
	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(keyServer),
	})
	if err := signup.MakeRequest(cfg); err != nil {
		s.Exit(err)
	}
	fmt.Fprintf(s.Stderr, "A signup email has been sent to %q,\n", cfg.UserName())
	fmt.Fprintf(s.Stderr, "please read it for further instructions.\n")
}

type configData struct {
	UserName        upspin.UserName
	Key, Store, Dir *upspin.Endpoint
	Packing         string
	SecretDir       string
}

var configTemplate = template.Must(template.New("config").Parse(`
{{with .Key}}keyserver: {{.}}
{{end}}
username: {{.UserName}}
storeserver: {{.Store}}
dirserver: {{.Dir}}
packing: {{.Packing}}
{{with .SecretDir}}secrets: {{.}}
{{end}}
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
