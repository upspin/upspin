// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"

	"upspin.io/flags"
	"upspin.io/upspin"
)

// This file implements the initial configuration for a new domain.

func (s *State) setupdomain(args ...string) {
	const (
		help = `
Setupdomain generates keys and config files for the Upspin users upspin-dir@domain
and upspin-store@domain, and generates a signature to be added as a DNS TXT record
to prove that the calling Upspin user has control over domain.

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
	host := fs.String("host", "", "host `name` of upspinserver (empty means store.domain, dir.domain)")
	s.parseFlags(fs, args, help, "[-project=<gcp_project_name>] setupdomain [-where=$HOME/upspin/deploy] [-host=hostname] <domain>")
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

	if *host != "" {
		s.setuphost(*where, domain, *host, *curveName)
		return
	}

	var (
		dirServerPath   = filepath.Join(*where, domain, "dirserver")
		storeServerPath = filepath.Join(*where, domain, "storeserver")
		dirConfig       = filepath.Join(dirServerPath, "config")
		storeConfig     = filepath.Join(storeServerPath, "config")
	)

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

	// Generate config files for those users.
	dirEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("dir." + domain + ":443"),
	}
	storeEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("store." + domain + ":443"),
	}
	var dirBody bytes.Buffer
	if err := configTemplate.Execute(&dirBody, configData{
		UserName:  upspin.UserName("upspin-dir@" + domain),
		Store:     &storeEndpoint,
		Dir:       &dirEndpoint,
		SecretDir: dirServerPath,
		Packing:   "symm",
	}); err != nil {
		s.exit(err)
	}
	if err := ioutil.WriteFile(dirConfig, dirBody.Bytes(), 0644); err != nil {
		s.exit(err)
	}
	var storeBody bytes.Buffer
	if err := configTemplate.Execute(&storeBody, configData{
		UserName:  upspin.UserName("upspin-store@" + domain),
		Store:     &storeEndpoint,
		Dir:       &dirEndpoint,
		SecretDir: storeServerPath,
		Packing:   "plain",
	}); err != nil {
		s.exit(err)
	}
	if err := ioutil.WriteFile(storeConfig, storeBody.Bytes(), 0644); err != nil {
		s.exit(err)
	}

	// Generate signature.
	msg := "upspin-domain:" + domain + "-" + string(s.config.UserName())
	sig, err := s.config.Factotum().Sign([]byte(msg))
	if err != nil {
		s.exit(err)
	}

	err = setupDomainTemplate.Execute(os.Stdout, setupDomainData{
		Dir:       filepath.Join(*where, flags.Project),
		Where:     *where,
		Domain:    domain,
		Project:   flags.Project,
		UserName:  s.config.UserName(),
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
Keys and config files for the users
	upspin-dir@{{.Domain}}
	upspin-store@{{.Domain}}
were generated and placed under the directory:
	{{.Dir}}

To prove that {{.UserName}} is the owner of {{.Domain}},
add the following record to {{.Domain}}'s DNS zone:

	NAME	TYPE	TTL	DATA
	@	TXT	15m	upspin:{{.Signature}}

Once the DNS change propagates the key server will use the TXT record to verify
that {{.UserName}} is authorized to register users under {{.Domain}}.
To register the users listed above, run this command:

	$ upspin -project={{.Project}} setupdomain -where={{.Where}} -put-users {{.Domain}}

`))

func (s *State) setuphost(where, domain, host, curve string) {
	cfgPath := filepath.Join(where, domain)
	s.shouldNotExist(cfgPath)
	s.mkdirAllLocal(cfgPath)

	// Generate and write keys for the server user.
	var noProquint string
	pub, pri, _, err := createKeys(curve, noProquint)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(cfgPath, pub, pri)
	if err != nil {
		s.exit(err)
	}

	// Generate and write symmetric key for DirServer data.
	var symmSecret [32]byte
	_, err = rand.Read(symmSecret[:])
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(cfgPath, "symmsecret.upspinkey"), symmSecret[:], 0600)
	if err != nil {
		s.exit(err)
	}

	// Generate signature.
	msg := "upspin-domain:" + domain + "-" + string(s.config.UserName())
	sig, err := s.config.Factotum().Sign([]byte(msg))
	if err != nil {
		s.exit(err)
	}

	// Write server config file.
	s.writeServerConfig(cfgPath, &ServerConfig{
		Addr: upspin.NetAddr(host + ":443"),
		User: upspin.UserName("upspin@" + domain),
	})

	err = setupHostTemplate.Execute(os.Stdout, setupDomainData{
		Dir:       cfgPath,
		Where:     where,
		Domain:    domain,
		Project:   flags.Project,
		UserName:  s.config.UserName(),
		Signature: fmt.Sprintf("%x-%x", sig.R, sig.S),
	})
	if err != nil {
		s.exit(err)
	}
}

var setupHostTemplate = template.Must(template.New("setuphost").Parse(`
Domain configuration and keys for the user
	upspin@{{.Domain}}
were generated and placed under the directory:
	{{.Dir}}

To prove that {{.UserName}} is the owner of {{.Domain}},
add the following record to {{.Domain}}'s DNS zone:

	NAME	TYPE	TTL	DATA
	@	TXT	15m	upspin:{{.Signature}}

Once the DNS change propagates the key server will use the TXT record to verify
that {{.UserName}} is authorized to register users under {{.Domain}}.

TODO: describe next steps
- setupstorage
- setupserver
`))

const serverConfigFile = "serverconfig.json"

type ServerConfig struct {
	Addr   upspin.NetAddr
	User   upspin.UserName
	Bucket string
}

func (s *State) readServerConfig(cfgPath string) *ServerConfig {
	cfgFile := filepath.Join(cfgPath, serverConfigFile)
	b, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.exitf("No server config file found at %q.\nRun 'upspin setupdomain' first.", cfgFile)
		}
		s.exit(err)
	}
	cfg := &ServerConfig{}
	if err := json.Unmarshal(b, cfg); err != nil {
		s.exit(err)
	}
	return cfg
}

func (s *State) writeServerConfig(cfgPath string, cfg *ServerConfig) {
	cfgFile := filepath.Join(cfgPath, serverConfigFile)
	b, err := json.Marshal(cfg)
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(cfgFile, b, 0644)
	if err != nil {
		s.exit(err)
	}
}
