// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/rand"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"

	yaml "gopkg.in/yaml.v2"

	"upspin.io/context"
	"upspin.io/flags"
	"upspin.io/upspin"
)

// This file implements the initial configuration for a new domain.

func (s *State) setupdomain(args ...string) {
	const (
		help = `
Setupdomain generates keys and rc files for the Upspin users upspin-dir@domain
and upspin-store@domain, and generates a signature to be added as a DNS TXT
record to prove that the calling Upspin user has control over domain.

If any state exists at the given location (-where) then the command aborts.

If you intend to deploy to a Google Cloud Platform project you must specify the
project ID with -project. This permits later steps to find the generated keys
and configuration files.

TODO: how to complete the process with 'upspin user -put'

Once the domain has been set up and its servers deployed, use setupwriters to
set access controls.
`
	)
	fs := flag.NewFlagSet("setupdomain", flag.ExitOnError)
	where := fs.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	curveName := fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	putUsers := fs.Bool("put-users", false, "put server users to the key server")
	s.parseFlags(fs, args, help, "[-project=<gcp_project_name>] setupdomain [-where=$HOME/upspin/deploy] <domain_name>")
	if fs.NArg() != 1 {
		fs.Usage()
	}
	if *where == "" {
		s.failf("the -where flag must not be empty")
		fs.Usage()
	}
	domain := fs.Arg(0)
	if domain == "" {
		s.failf("domain must be provided")
		fs.Usage()
	}
	switch *curveName {
	case "p256", "p384", "p521":
		// OK
	default:
		s.exitf("no such curve %q", *curveName)
	}

	var (
		dirServerPath   = filepath.Join(*where, flags.Project, "dirserver")
		storeServerPath = filepath.Join(*where, flags.Project, "storeserver")
		dirRC           = filepath.Join(dirServerPath, "rc")
		storeRC         = filepath.Join(storeServerPath, "rc")
	)

	if *putUsers {
		dirFile, dirUser, err := writeUserFile(dirRC)
		if err != nil {
			s.exit(err)
		}
		storeFile, storeUser, err := writeUserFile(storeRC)
		if err != nil {
			s.exit(err)
		}
		s.user("-put", "-in", dirFile)
		os.Remove(dirFile)
		s.user("-put", "-in", storeFile)
		os.Remove(storeFile)
		fmt.Printf("Successfully put %q and %q to the key server.\n", dirUser, storeUser)
		return
	}

	s.shouldNotExist(dirServerPath)
	s.shouldNotExist(storeServerPath)
	s.mkdirAllLocal(dirServerPath)
	s.mkdirAllLocal(storeServerPath)

	// Generate keys for the dirserver and the storeserver.
	var noProquint string
	dirPublic, dirPrivate, _, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	storePublic, storePrivate, _, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(dirServerPath, dirPublic, dirPrivate)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(storeServerPath, storePublic, storePrivate)
	if err != nil {
		s.exit(err)
	}

	// Generate and write symmetric key for DirServer data.
	var symmSecret [32]byte
	_, err = rand.Read(symmSecret[:])
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(dirServerPath, "symmsecret.upspinkey"), symmSecret[:], 0600)
	if err != nil {
		s.exit(err)
	}

	// Generate rc files for those users.
	dirEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("dir." + domain + ":443"),
	}
	storeEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("store." + domain + ":443"),
	}
	var dirBody bytes.Buffer
	if err := rcTemplate.Execute(&dirBody, rcData{
		UserName:  upspin.UserName("upspin-dir@" + domain),
		Store:     &storeEndpoint,
		Dir:       &dirEndpoint,
		SecretDir: dirServerPath,
		Packing:   "symm",
	}); err != nil {
		s.exit(err)
	}
	if err := ioutil.WriteFile(dirRC, dirBody.Bytes(), 0644); err != nil {
		s.exit(err)
	}
	var storeBody bytes.Buffer
	if err := rcTemplate.Execute(&storeBody, rcData{
		UserName:  upspin.UserName("upspin-store@" + domain),
		Store:     &storeEndpoint,
		Dir:       &dirEndpoint,
		SecretDir: storeServerPath,
		Packing:   "plain",
	}); err != nil {
		s.exit(err)
	}
	if err := ioutil.WriteFile(storeRC, storeBody.Bytes(), 0644); err != nil {
		s.exit(err)
	}

	// Generate signature.
	msg := "upspin-domain:" + domain + "-" + string(s.context.UserName())
	sig, err := s.context.Factotum().Sign([]byte(msg))
	if err != nil {
		s.exit(err)
	}

	err = setupDomainTemplate.Execute(os.Stdout, setupDomainData{
		Dir:       filepath.Join(*where, flags.Project),
		Where:     *where,
		Domain:    domain,
		Project:   flags.Project,
		UserName:  s.context.UserName(),
		Signature: fmt.Sprintf("%x-%x", sig.R, sig.S),
	})
	if err != nil {
		s.exit(err)
	}
}

type setupDomainData struct {
	Dir, Where string
	Domain     string
	Project    string
	UserName   upspin.UserName
	Signature  string
}

var setupDomainTemplate = template.Must(template.New("setupdomain").Parse(`
Keys and rc files for the users
	upspin-dir@{{.Domain}}
	upspin-store@{{.Domain}}
were generated and placed under the directory:
	{{.Dir}}

To prove that {{.UserName}} is the owner of {{.Domain}},
add the following record to {{.Domain}}'s DNS zone:

	NAME	TYPE	TTL	DATA
	@	TXT	1h	upspin:{{.Signature}}

Once the DNS change propagates the key server will use the TXT record to verify
that {{.UserName}} is authorized to register users under {{.Domain}}.
To register the users listed above, run this command:

	$ upspin -project={{.Project}} setupdomain -where={{.Where}} -put-users {{.Domain}}

`))

// writeUserFile reads the specified rc file and writes a YAML-encoded
// upspin.User to userFile. It also returns the username.
func writeUserFile(rcFile string) (userFile string, u upspin.UserName, err error) {
	ctx, err := context.FromFile(rcFile)
	if err != nil {
		return "", "", err
	}
	b, err := yaml.Marshal(context.User(ctx))
	if err != nil {
		return "", "", err
	}
	f, err := ioutil.TempFile("", "setupdomain-user")
	if err != nil {
		return "", "", err
	}
	if _, err := f.Write(b); err != nil {
		os.Remove(f.Name())
		return "", "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", "", err
	}
	return f.Name(), ctx.UserName(), nil
}
