// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/context"
	"upspin.io/upspin"
	"upspin.io/user"
)

func (s *State) signup(args ...string) {
	const help = `
Signup registers new users with Upspin. It creates a private/public
key pair, stores the private key locally, and prepares to store the
private key with the public upspin key server. It writes an intial
"rc" file into $HOME/upspin/rc, holding the username and the location
of the key server.

As the final step, it writes the contents of a mail message to
standard output. This message contains the public key to be registered
with the key server. After running signup, the new user must mail
this message to signup@key.upspin.io to complete the signup process.

Once this is done, the user should update the rc file to hold the
network addresses of the directory and store servers to use; the
local adminstrator can provide this information.

TODO: The last step should be done automatically. Perhaps signup
should take those two addresses as arguments.
`
	fs := flag.NewFlagSet("signup", flag.ExitOnError)
	force := fs.Bool("force", false, "create a new user even if keys and rc file exist")
	rcFile := fs.String("rc", "upspin/rc", "location of the rc file")
	s.parseFlags(fs, args, help, "signup email_address")
	if fs.NArg() != 1 {
		fs.Usage()
	}

	// User must have a home dir in their native OS.
	homedir, err := context.Homedir()
	if err != nil {
		s.exit(err)
	}

	uname, _, domain, err := user.Parse(upspin.UserName(fs.Arg(0)))
	if err != nil {
		s.exit(err)
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
	_, err = context.FromFile(*rcFile)
	if err == nil && !*force {
		s.exitf("%s already exists", *rcFile)
	}

	// Create an rc file for this new user.
	const (
		rcTemplate = `username: %s

### Please update these entries to refer to your servers
### and remove the leading # character.
# storeserver: remote,store.example.com
# dirserver: remote,dir.example.com`

		defaultKeyServer = "remote,key.upspin.io:443"
	)

	rcContents := fmt.Sprintf(rcTemplate, userName)
	err = ioutil.WriteFile(*rcFile, []byte(rcContents), 0640)
	if err != nil {
		s.exit(err)
	}

	// Generate a new key.
	s.keygen()
	// TODO: write better instructions.
	fmt.Println("Write down the command above. You will need it if you lose your keys.")
	// Now load the context. This time it should succeed.
	ctx, err := context.FromFile(*rcFile)
	if err != nil {
		s.exit(err)
	}

	f := ctx.Factotum()
	if f == nil {
		s.exitf("no factotum available")
	}
	pubKey := strings.TrimSpace(string(f.PublicKey()))

	// Sign the username and key.
	sig, err := f.Sign([]byte(string(ctx.UserName()) + pubKey))
	if err != nil {
		s.exit(err)
	}

	const mailTemplate = `I am %s;
My public key is:
%s;
Signature:
%s:%s
`
	keyLines := strings.Replace(pubKey, "\n", ";\n", 3)
	msg := fmt.Sprintf(mailTemplate, ctx.UserName(), keyLines,
		sig.R.String(), sig.S.String())

	fmt.Printf("\nTo complete your registration, send email to signup@key.upspin.io with the following contents:\n\n%s\n", msg)
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
