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
	"strings"
	"text/template"

	"upspin.io/config"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) signup(args ...string) {
	const help = `
Signup registers new users with Upspin. It creates a private/public key pair,
stores the private key locally, and prepares to store the private key with the
public upspin key server. It writes an "rc" file into $HOME/upspin/rc, holding
the username and the location of the directory and store servers.

By default, signup creates new keys with the p256 cryptographic curve set.
The -curve and -secretseed flags allow the user to control the curve or to
recreate or reuse prior keys.

As the final step, it writes the contents of a mail message to standard output.
This message contains the information to be registered with the key server.
After running signup, the new user must mail this message to
signup@key.upspin.io to complete the signup process.
`
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	var (
		force       = fs.Bool("force", false, "create a new user even if keys and rc file exist")
		rcFile      = fs.String("rc", "upspin/rc", "location of the rc file")
		where       = fs.String("where", filepath.Join(os.Getenv("HOME"), ".ssh"), "`directory` to store keys")
		dirServer   = fs.String("dir", "", "DirServer `address`")
		storeServer = fs.String("store", "", "StoreServer `address`")
	)
	// Used only in keygen.
	fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	fs.String("secretseed", "", "128 bit secret `seed` in proquint format")

	s.parseFlags(fs, args, help, "signup [-dir=address] [-store=address] [-secretseed=seed] [-curve=p256] [email]")
	if fs.NArg() != 1 {
		fs.Usage()
	}
	if *dirServer == "" || *storeServer == "" {
		s.failf("-dir and -store must both be provided")
		fs.Usage()
	}

	// User must have a home dir in the local OS.
	homedir, err := config.Homedir()
	if err != nil {
		s.exit(err)
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

	// Figure out location of the rc file.
	if !filepath.IsAbs(*rcFile) {
		*rcFile = filepath.Join(homedir, *rcFile)
	}
	env := os.Environ()
	wipeUpspinEnvironment()
	defer restoreEnvironment(env)

	// Verify if we have an rc file.
	_, err = config.FromFile(*rcFile)
	if err == nil && !*force {
		s.exitf("%s already exists", *rcFile)
	}

	// Write the rc file.
	var rcContents bytes.Buffer
	err = rcTemplate.Execute(&rcContents, rcData{
		UserName:  userName,
		Dir:       dirEndpoint,
		Store:     storeEndpoint,
		SecretDir: *where,
		Packing:   "ee",
	})
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(*rcFile, rcContents.Bytes(), 0640)
	if err != nil {
		// Directory doesn't exist, perhaps.
		if !os.IsNotExist(err) {
			s.exitf("cannot create %s: %v", *rcFile, err)
		}
		dir := filepath.Dir(*rcFile)
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			// Looks like the directory exists, so stop now and report original error.
			s.exitf("cannot create %s: %v", *rcFile, err)
		}
		if mkdirErr := os.Mkdir(dir, 0700); mkdirErr != nil {
			s.exitf("cannot make directory %s: %v", dir, mkdirErr)
		}
		err = ioutil.WriteFile(*rcFile, rcContents.Bytes(), 0640)
		if err != nil {
			s.exit(err)
		}
	}
	fmt.Println("Configuration file written to:")
	fmt.Printf("\t%s\n\n", *rcFile)

	// Generate a new key.
	s.keygenCommand(fs)

	// Now load the config. This time it should succeed.
	cfg, err := config.FromFile(*rcFile)
	if err != nil {
		s.exit(err)
	}

	f := cfg.Factotum()
	if f == nil {
		s.exitf("no factotum available")
	}
	pubKey := strings.TrimSpace(string(f.PublicKey()))

	// Sign the username, key, and dir and store endpoints.
	sig, err := f.Sign([]byte(string(cfg.UserName()) + pubKey + dirEndpoint.String() + storeEndpoint.String()))
	if err != nil {
		s.exit(err)
	}

	var msg bytes.Buffer
	err = mailTemplate.Execute(&msg, mailData{
		UserName:  userName,
		PublicKey: strings.Replace(pubKey, "\n", ";\n", 3),
		Dir:       dirEndpoint,
		Store:     storeEndpoint,
		Signature: sig.R.String() + ";\n" + sig.S.String(),
	})
	if err != nil {
		s.exit(err)
	}

	fmt.Println("To register your key with the key server,")
	fmt.Println("copy this email message and send it to signup@key.upspin.io:")
	fmt.Printf("%s\n", &msg)
}

type rcData struct {
	UserName   upspin.UserName
	Store, Dir *upspin.Endpoint
	SecretDir  string
	Packing    string
}

var rcTemplate = template.Must(template.New("rc").Parse(`
username: {{.UserName}}
secrets: {{.SecretDir}}
storeserver: {{.Store}}
dirserver: {{.Dir}}
packing: {{.Packing}}
`))

type mailData struct {
	UserName   upspin.UserName
	PublicKey  string
	Dir, Store *upspin.Endpoint
	Signature  string
}

var mailTemplate = template.Must(template.New("mail").Parse(`
I am {{.UserName}};

My public key is:
{{.PublicKey}};

My directory server is:
{{.Dir}};

My store server is:
{{.Store}};

Signature:
{{.Signature}};

(End of message.)
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
