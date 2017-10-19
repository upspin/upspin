// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"upspin.io/pack"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

func init() {
	inTest = true
}

type expectations struct {
	username    upspin.UserName
	keyserver   upspin.Endpoint
	dirserver   upspin.Endpoint
	storeserver upspin.Endpoint
	packing     upspin.Packing
	secrets     string
	cmdflags    string
}

var secretsDir string

func init() {
	cwd, _ := os.Getwd()
	secretsDir = filepath.Join(cwd, "../key/testdata/user1")
}

func TestInitConfig(t *testing.T) {
	expect := expectations{
		username:    "p@google.com",
		keyserver:   upspin.Endpoint{Transport: upspin.InProcess, NetAddr: ""},
		dirserver:   upspin.Endpoint{Transport: upspin.Remote, NetAddr: "who.knows:1234"},
		storeserver: upspin.Endpoint{Transport: upspin.Remote, NetAddr: "who.knows:1234"},
		packing:     upspin.EEPack,
		secrets:     secretsDir,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestDefaults(t *testing.T) {
	expect := expectations{
		username:  "noone@nowhere.org",
		keyserver: defaultKeyEndpoint,
		packing:   upspin.EEPack,
		secrets:   secretsDir,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestBadKey(t *testing.T) {
	// TODO(adg): with the addition of Value to the upspin.Config interface
	// it is not clear whether the "unknown" keys should trigger errors.
	// To do this we'd need to make the config package aware of all valid
	// config keys, which may or may not be desirable.
	t.Skip("not sure whether this should be the expected behavior")

	// "name=" should be "username=".
	const config = `name: p@google.com
packing: ee
keyserver: inprocess
dirserver: inprocess
storeserver: inprocess`
	_, err := InitConfig(strings.NewReader(config))
	if err == nil {
		t.Fatalf("expected error, got none")
	}
	if !strings.Contains(err.Error(), "unrecognized key") {
		t.Fatalf("expected bad key error; got %q", err)
	}
}

func TestCmdFlags(t *testing.T) {
	config := `
keyserver: key.example.com
cmdflags:
 cacheserver:
  cachedir: /tmp
  cachesize: 1000000000
 upspinfs:
  cachedir: /tmp
dirserver: remote,dir.example.com
storeserver: store.example.com:8080
secrets: ` + secretsDir + "\n"
	expect := expectations{
		username:    "noone@nowhere.org",
		packing:     upspin.EEPack,
		keyserver:   upspin.Endpoint{Transport: upspin.Remote, NetAddr: "key.example.com:443"},
		dirserver:   upspin.Endpoint{Transport: upspin.Remote, NetAddr: "dir.example.com:443"},
		storeserver: upspin.Endpoint{Transport: upspin.Remote, NetAddr: "store.example.com:8080"},
		cmdflags: `cacheserver:
  cachedir: /tmp
  cachesize: 1000000000
upspinfs:
  cachedir: /tmp`,
	}
	testConfig(t, &expect, config)
}

func TestSetFlagValues(t *testing.T) {
	// Define flags with defaults.
	flag.CommandLine = flag.NewFlagSet("hooha", flag.ContinueOnError)
	cacheSizeFlag := flag.Int64("cachesize", 5e9, "max disk `bytes` for cache")
	writethroughFlag := flag.Bool("writethrough", false, "make storage cache writethrough")

	// Expected values
	expectedSize := int64(4000000000)
	expectedWT := true

	configuration := `
secrets: ` + secretsDir + `
cmdflags:
 cacheserver:
  cachesize: ` + fmt.Sprintf("%d", expectedSize) + `
  writethrough: ` + fmt.Sprintf("%t", expectedWT) + `
`
	config, err := InitConfig(strings.NewReader(configuration))
	if err != nil {
		t.Fatalf("could not parse config %v: %v", configuration, err)
	}
	if err := SetFlagValues(config, "cacheserver"); err != nil {
		t.Fatalf("could not apply config flags %v: %v", configuration, err)
	}
	if *cacheSizeFlag != expectedSize {
		t.Fatalf("cachesize got %v, expected %v", *cacheSizeFlag, expectedSize)
	}
	if *writethroughFlag != expectedWT {
		t.Fatalf("cachesize got %v, expected %v", *cacheSizeFlag, expectedSize)
	}

	// No flags present for upspinfs, and that's fine.
	if err := SetFlagValues(config, "upspinfs"); err != nil {
		t.Fatalf("SetFlagValues should not have failed for upspinfs: %v", err)
	}

	// Add an undefined flag and expect an error from the apply.
	configuration = `
secrets: ` + secretsDir + `
cmdflags:
 cacheserver:
  cachesize: ` + fmt.Sprintf("%d", expectedSize) + `
  writethrough: ` + fmt.Sprintf("%v", expectedWT) + `
  cachedir: /tmp
`
	config, err = InitConfig(strings.NewReader(configuration))
	if err != nil {
		t.Fatalf("could not parse config %v: %v", configuration, err)
	}
	if err := SetFlagValues(config, "cacheserver"); err == nil {
		t.Fatalf("SetFlagValues should have failed %v", configuration)
	}
}

func TestNoSecrets(t *testing.T) {
	expect := expectations{
		username: "bob@google.com",
		packing:  upspin.EEPack,
		secrets:  "none",
	}
	r := strings.NewReader(makeConfig(&expect))
	cfg, err := InitConfig(r)
	if err != ErrNoFactotum {
		t.Errorf("InitConfig returned error %v, want %v", err, ErrNoFactotum)
	}
	if cfg != nil && cfg.Factotum() != nil {
		t.Errorf("InitConfig returned a non-nil Factotum")
	}
}

func TestEndpointDefaults(t *testing.T) {
	config := `
keyserver: key.example.com
dirserver: remote,dir.example.com
storeserver: store.example.com:8080
secrets: ` + secretsDir + "\n"
	expect := expectations{
		username:    "noone@nowhere.org",
		packing:     upspin.EEPack,
		keyserver:   upspin.Endpoint{Transport: upspin.Remote, NetAddr: "key.example.com:443"},
		dirserver:   upspin.Endpoint{Transport: upspin.Remote, NetAddr: "dir.example.com:443"},
		storeserver: upspin.Endpoint{Transport: upspin.Remote, NetAddr: "store.example.com:8080"},
	}
	testConfig(t, &expect, config)
}

func makeConfig(expect *expectations) string {
	var buf bytes.Buffer

	if expect.username != "" {
		fmt.Fprintf(&buf, "username: %s\n", expect.username)
	}

	var zero upspin.Endpoint
	if expect.keyserver != zero {
		fmt.Fprintf(&buf, "keyserver: %s\n", expect.keyserver)
	}
	if expect.storeserver != zero {
		fmt.Fprintf(&buf, "storeserver: %s\n", expect.storeserver)
	}
	if expect.dirserver != zero {
		fmt.Fprintf(&buf, "dirserver: %s\n", expect.dirserver)
	}

	fmt.Fprintf(&buf, "packing: %s\n", pack.Lookup(expect.packing))

	if expect.secrets != "" {
		fmt.Fprintf(&buf, "secrets: %s\n", expect.secrets)
	}

	return buf.String()
}

func testConfig(t *testing.T, expect *expectations, configuration string) {
	config, err := InitConfig(strings.NewReader(configuration))
	if err != nil {
		t.Fatalf("could not parse config %v: %v", configuration, err)
	}
	if config.UserName() != expect.username {
		t.Errorf("name: got %v expected %v", config.UserName(), expect.username)
	}
	tests := []struct {
		expected upspin.Endpoint
		got      upspin.Endpoint
	}{
		{expect.keyserver, config.KeyEndpoint()},
		{expect.dirserver, config.DirEndpoint()},
		{expect.storeserver, config.StoreEndpoint()},
	}
	for i, test := range tests {
		if test.expected != test.got {
			t.Errorf("%d: got %s expected %v", i, test.got, test.expected)
		}
	}
	if config.Packing() != expect.packing {
		t.Errorf("got %v expected %v", config.Packing(), expect.packing)
	}
	cmdflags := config.Value("cmdflags")
	if !reflect.DeepEqual(expect.cmdflags, cmdflags) {
		t.Errorf("got cmdflags\n\t%#v\nexpected\n\t%#v", cmdflags, expect.cmdflags)
	}
}
