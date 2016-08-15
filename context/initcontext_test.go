// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//Package context creates a client context from various sources.
package context

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"upspin.io/pack"
	"upspin.io/upspin"

	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

func init() {
	inTest = true
}

var once sync.Once

type expectations struct {
	username    upspin.UserName
	keyserver   upspin.Endpoint
	dirserver   upspin.Endpoint
	storeserver upspin.Endpoint
	packing     upspin.Packing
	secrets     string
}

type envs struct {
	username    string
	keyserver   string
	dirserver   string
	storeserver string
	packing     string
	secrets     string
}

// Endpoint is a helper to make it easier to build vet-error-free upspin.Endpoints.
func Endpoint(t upspin.Transport, n upspin.NetAddr) upspin.Endpoint {
	return upspin.Endpoint{
		Transport: t,
		NetAddr:   n,
	}
}

func TestInitContext(t *testing.T) {
	expect := expectations{
		username:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.Remote, "who.knows:1234"),
		storeserver: Endpoint(upspin.Remote, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestComments(t *testing.T) {
	expect := expectations{
		username:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.Remote, "who.knows:1234"),
		storeserver: Endpoint(upspin.Remote, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	testConfig(t, &expect, makeCommentedConfig(&expect))
}

func TestDefaults(t *testing.T) {
	expect := expectations{
		username: "noone@nowhere.org",
		packing:  upspin.PlainPack,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestBadKey(t *testing.T) {
	// "name=" should be "username=".
	const config = `name=p@google.com
packing=ee
keyserver=inprocess
dirserver=inprocess
storeserver=inprocess`
	_, err := InitContext(strings.NewReader(config))
	if err == nil {
		t.Fatalf("expected error, got none")
	}
	if !strings.Contains(err.Error(), "unrecognized key") {
		t.Fatalf("expected bad key error; got %q", err)
	}
}

func TestEnv(t *testing.T) {
	expect := expectations{
		username:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.Remote, "who.knows:1234"),
		storeserver: Endpoint(upspin.Remote, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	config := makeConfig(&expect)
	expect.username = "quux"
	os.Setenv("upspinusername", string(expect.username))
	expect.keyserver = Endpoint(upspin.InProcess, "")
	expect.dirserver = Endpoint(upspin.Remote, "who.knows:1234")
	expect.storeserver = Endpoint(upspin.Remote, "who.knows:1234")
	os.Setenv("upspinkeyserver", expect.keyserver.String())
	os.Setenv("upspindirserver", expect.dirserver.String())
	os.Setenv("upspinstoreserver", expect.storeserver.String())
	expect.packing = upspin.PlainPack
	os.Setenv("upspinpacking", pack.Lookup(expect.packing).String())
	testConfig(t, &expect, config)
}

func TestBadEnv(t *testing.T) {
	expect := expectations{
		username:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.Remote, "who.knows:1234"),
		storeserver: Endpoint(upspin.Remote, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	config := makeConfig(&expect)
	os.Setenv("upspinuser", string(expect.username)) // Should be upspinusername.
	_, err := InitContext(strings.NewReader(config))
	os.Unsetenv("upspinuser")
	if err == nil {
		t.Fatalf("expected error, got none")
	}
	if !strings.Contains(err.Error(), "unrecognized environment variable") {
		t.Fatalf("expected bad env var error; got %q", err)
	}
}

func TestNoSecrets(t *testing.T) {
	expect := expectations{
		username: "bob@google.com",
		packing:  upspin.PlainPack,
		secrets:  "none",
	}
	r := strings.NewReader(makeConfig(&expect))
	ctx, err := InitContext(r)
	if err != ErrNoFactotum {
		t.Errorf("InitContext returned error %v, want %v", err, ErrNoFactotum)
	}
	if ctx != nil && ctx.Factotum() != nil {
		t.Errorf("InitContext returned a non-nil Factotum")
	}
}

func makeConfig(expect *expectations) string {
	var buf bytes.Buffer

	if expect.username != "" {
		fmt.Fprintf(&buf, "username = %s\n", expect.username)
	}

	var zero upspin.Endpoint
	if expect.keyserver != zero {
		fmt.Fprintf(&buf, "keyserver = %s\n", expect.keyserver)
	}
	if expect.storeserver != zero {
		fmt.Fprintf(&buf, "storeserver = %s\n", expect.storeserver)
	}
	if expect.dirserver != zero {
		fmt.Fprintf(&buf, "dirserver = %s\n", expect.dirserver)
	}

	fmt.Fprintf(&buf, "packing = %s\n", pack.Lookup(expect.packing))

	if expect.secrets != "" {
		fmt.Fprintf(&buf, "secrets = %s\n", expect.secrets)
	}

	return buf.String()
}

func makeCommentedConfig(expect *expectations) string {
	return fmt.Sprintf("# Line one is a comment\nusername = %s # Ignore this.\nkeyserver= %s\nstoreserver = %s\n  dirserver =%s   \npacking=%s #Ignore this",
		expect.username,
		expect.keyserver,
		expect.storeserver,
		expect.dirserver,
		pack.Lookup(expect.packing).String())
}

func saveEnvs(e *envs) {
	e.username = os.Getenv("upspinusername")
	e.keyserver = os.Getenv("upspinkeyserver")
	e.dirserver = os.Getenv("upspindirserver")
	e.storeserver = os.Getenv("upspinstoreserver")
	e.packing = os.Getenv("upspinpacking")
	e.secrets = os.Getenv("upspinsecrets")
}

func restoreEnvs(e *envs) {
	os.Setenv("upspinusername", e.username)
	os.Setenv("upspinkeyserver", e.keyserver)
	os.Setenv("upspindirserver", e.dirserver)
	os.Setenv("upspinstoreserver", e.storeserver)
	os.Setenv("upspinpacking", e.packing)
	os.Setenv("upspinsecrets", e.secrets)
}

func resetEnvs() {
	var emptyEnv envs
	restoreEnvs(&emptyEnv)
}

func TestMain(m *testing.M) {
	var e envs
	saveEnvs(&e)
	resetEnvs()
	code := m.Run()
	restoreEnvs(&e)
	os.Exit(code)
}

func testConfig(t *testing.T, expect *expectations, config string) {
	context, err := InitContext(strings.NewReader(config))
	if err != nil {
		t.Fatalf("could not parse config %v: %v", config, err)
	}
	if context.UserName() != expect.username {
		t.Errorf("name: got %v expected %v", context.UserName(), expect.username)
	}
	tests := []struct {
		expected upspin.Endpoint
		got      upspin.Endpoint
	}{
		{expect.keyserver, context.KeyEndpoint()},
		{expect.dirserver, context.DirEndpoint()},
		{expect.storeserver, context.StoreEndpoint()},
	}
	for i, test := range tests {
		if test.expected != test.got {
			t.Errorf("%d: got %s expected %v", i, test.got, test.expected)
		}
	}
	if context.Packing() != expect.packing {
		t.Errorf("got %v expected %v", context.Packing(), expect.packing)
	}
}
