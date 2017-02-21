// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"

	yaml "gopkg.in/yaml.v2"

	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/upspin"
)

// This file implements the initial configuration for a new domain.

func (s *State) setupdomain(args ...string) {
	const (
		help = `
Setupdomain is the first step in setting up an upspinserver.
The next steps are 'setupstorage' and 'setupserver'.

It generates keys and config files for Upspin server users, placing them in
$where/$domain (the values of the -where and -domain flags substitute for
$where and $domain respectively) and generates a signature that proves that the
calling Upspin user has control over domain.

If the -cluster flag is specified, keys for upspin-dir@domain and
upspin-store@domain are created instead. This flag should be used when setting
up a domain that will run its directory and store servers separately, requiring
separate users to adminster each one. When -cluster is not specified, keys for
a single user (upspin@domain) are generated.

If any state exists at the given location (-where) then the command aborts.
`
	)
	fs := flag.NewFlagSet("setupdomain", flag.ExitOnError)
	where := fs.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	domain := fs.String("domain", "", "domain `name` for this Upspin installation")
	curveName := fs.String("curve", "p256", "cryptographic curve `name`: p256, p384, or p521")
	putUsers := fs.Bool("put-users", false, "put server users to the key server")
	cluster := fs.Bool("cluster", false, "generate keys for upspin-dir@domain and upspin-store@domain (default is upspin@domain only)")
	s.parseFlags(fs, args, help, "setupdomain [-where=$HOME/upspin/deploy] [-cluster] -domain=<name>")
	if *where == "" {
		s.failf("the -where flag must not be empty")
		fs.Usage()
	}
	if *domain == "" {
		s.failf("the -domain flag must be provided")
		fs.Usage()
	}
	switch *curveName {
	case "p256", "p384", "p521":
		// OK
	default:
		s.exitf("no such curve %q", *curveName)
	}

	if !*cluster {
		if *putUsers {
			s.exitf("the -put-users flag requires -cluster")
		}
		s.setuphost(*where, *domain, *curveName)
		return
	}

	var (
		baseDir         = filepath.Join(*where, *domain)
		dirServerPath   = filepath.Join(baseDir, "dirserver")
		storeServerPath = filepath.Join(baseDir, "storeserver")
		dirConfig       = filepath.Join(dirServerPath, "config")
		storeConfig     = filepath.Join(storeServerPath, "config")
	)

	if *putUsers {
		dirFile, dirUser, err := writeUserFile(dirConfig)
		if err != nil {
			s.exit(err)
		}
		storeFile, storeUser, err := writeUserFile(storeConfig)
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
	dirPublic, dirPrivate, dirProquint, err := createKeys(*curveName, noProquint)
	if err != nil {
		s.exit(err)
	}
	storePublic, storePrivate, storeProquint, err := createKeys(*curveName, noProquint)
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

	// Generate config files for those users.
	dirEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("dir." + *domain + ":443"),
	}
	storeEndpoint := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr("store." + *domain + ":443"),
	}
	var dirBody bytes.Buffer
	if err := configTemplate.Execute(&dirBody, configData{
		UserName:  upspin.UserName("upspin-dir@" + *domain),
		Store:     &storeEndpoint,
		Dir:       &dirEndpoint,
		SecretDir: dirServerPath,
		Packing:   "ee",
	}); err != nil {
		s.exit(err)
	}
	if err := ioutil.WriteFile(dirConfig, dirBody.Bytes(), 0644); err != nil {
		s.exit(err)
	}
	var storeBody bytes.Buffer
	if err := configTemplate.Execute(&storeBody, configData{
		UserName:  upspin.UserName("upspin-store@" + *domain),
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
	msg := "upspin-domain:" + *domain + "-" + string(s.config.UserName())
	sig, err := s.config.Factotum().Sign([]byte(msg))
	if err != nil {
		s.exit(err)
	}

	err = setupDomainTemplate.Execute(os.Stdout, setupDomainData{
		Dir:       baseDir,
		Where:     *where,
		Domain:    *domain,
		Project:   flags.Project,
		UserName:  s.config.UserName(),
		Signature: fmt.Sprintf("%x-%x", sig.R, sig.S),

		DirProquint:     dirProquint,
		StoreProquint:   storeProquint,
		DirServerPath:   dirServerPath,
		StoreServerPath: storeServerPath,
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

	// Used by setupDomain.
	DirProquint     string
	StoreProquint   string
	DirServerPath   string
	StoreServerPath string

	// Used by setupHost.
	Proquint string
}

var setupDomainTemplate = template.Must(template.New("setupdomain").Parse(`
Keys and config files for the users
	upspin-dir@{{.Domain}}
	upspin-store@{{.Domain}}
were generated and placed under the directory:
	{{.Dir}}
If you lose the keys you can re-create them by running these commands
	upspin keygen -where {{.DirServerPath}} -secretseed {{.DirProquint}}
	upspin keygen -where {{.StoreServerPath}} -secretseed {{.StoreProquint}}
Write them down and store them in a secure, private place.
Do not share your private keys or these commands with anyone.

To prove that {{.UserName}} is the owner of {{.Domain}},
add the following record to {{.Domain}}'s DNS zone:

	NAME	TYPE	TTL	DATA
	@	TXT	15m	upspin:{{.Signature}}

Once the DNS change propagates the key server will use the TXT record to verify
that {{.UserName}} is authorized to register users under {{.Domain}}.
To register the users listed above, run this command:

	$ upspin -project={{.Project}} setupdomain -where={{.Where}} -cluster -put-users {{.Domain}}

`))

// writeUserFile reads the specified config file and writes a YAML-encoded
// upspin.User to userFile. It also returns the username.
func writeUserFile(configFile string) (userFile string, u upspin.UserName, err error) {
	cfg, err := config.FromFile(configFile)
	if err != nil {
		return "", "", err
	}
	b, err := yaml.Marshal(config.User(cfg))
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
	return f.Name(), cfg.UserName(), nil
}

func (s *State) setuphost(where, domain, curve string) {
	cfgPath := filepath.Join(where, domain)
	s.shouldNotExist(cfgPath)
	s.mkdirAllLocal(cfgPath)

	// Generate and write keys for the server user.
	var noProquint string
	pub, pri, proquint, err := createKeys(curve, noProquint)
	if err != nil {
		s.exit(err)
	}
	err = writeKeys(cfgPath, pub, pri)
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
		User: upspin.UserName("upspin@" + domain),
	})

	err = setupHostTemplate.Execute(os.Stdout, setupDomainData{
		Dir:       cfgPath,
		Where:     where,
		Domain:    domain,
		Project:   flags.Project,
		UserName:  s.config.UserName(),
		Signature: fmt.Sprintf("%x-%x", sig.R, sig.S),

		Proquint: proquint,
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
If you lose the keys you can re-create them by running this command
	upspin keygen -where {{.Dir}} -secretseed {{.Proquint}}
Write this command down and store it in a secure, private place.
Do not share your private key or this command with anyone.

To prove that {{.UserName}} is the owner of {{.Domain}},
add the following record to {{.Domain}}'s DNS zone:

	NAME	TYPE	TTL	DATA
	@	TXT	15m	upspin:{{.Signature}}

Once the DNS change propagates the key server will use the TXT record to verify
that {{.UserName}} is authorized to register users under {{.Domain}}.

After that, the next step is to run 'upspin setupstorage'.
`))
