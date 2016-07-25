// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//Package context creates a client context from various sources.
package context

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"

	"upspin.io/pack"
	"upspin.io/upspin"

	// TODO _ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

var once sync.Once

type expectations struct {
	userName    upspin.UserName
	keyserver   upspin.Endpoint
	dirserver   upspin.Endpoint
	storeserver upspin.Endpoint
	packing     upspin.Packing
}

type envs struct {
	name        string
	keyserver   string
	dirserver   string
	storeserver string
	packing     string
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
		userName:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.GCP, "who.knows:1234"),
		storeserver: Endpoint(upspin.GCP, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestComments(t *testing.T) {
	expect := expectations{
		userName:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.GCP, "who.knows:1234"),
		storeserver: Endpoint(upspin.GCP, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	testConfig(t, &expect, makeCommentedConfig(&expect))
}

func TestDefaults(t *testing.T) {
	expect := expectations{
		userName: "noone@nowhere.org",
		packing:  upspin.PlainPack,
	}
	testConfig(t, &expect, makeConfig(&expect))
}

func TestEnv(t *testing.T) {
	expect := expectations{
		userName:    "p@google.com",
		keyserver:   Endpoint(upspin.InProcess, ""),
		dirserver:   Endpoint(upspin.GCP, "who.knows:1234"),
		storeserver: Endpoint(upspin.GCP, "who.knows:1234"),
		packing:     upspin.PlainPack, // TODO upspin.EEPack,
	}
	config := makeConfig(&expect)
	expect.userName = "quux"
	os.Setenv("upspinusername", string(expect.userName))
	expect.keyserver = Endpoint(upspin.InProcess, "")
	expect.dirserver = Endpoint(upspin.GCP, "who.knows:1234")
	expect.storeserver = Endpoint(upspin.GCP, "who.knows:1234")
	os.Setenv("upspinkeyserver", expect.keyserver.String())
	os.Setenv("upspindirserver", expect.dirserver.String())
	os.Setenv("upspinstoreserver", expect.storeserver.String())
	expect.packing = upspin.PlainPack
	os.Setenv("upspinpacking", pack.Lookup(expect.packing).String())
	testConfig(t, &expect, config)
}

func makeConfig(expect *expectations) string {
	var buf bytes.Buffer
	var zero upspin.Endpoint
	if expect.userName != "" {
		fmt.Fprintf(&buf, "username = %s\n", expect.userName)
	}
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
	return buf.String()
}

func makeCommentedConfig(expect *expectations) string {
	return fmt.Sprintf("# Line one is a comment\nusername = %s # Ignore this.\nkeyserver= %s\nstoreserver = %s\n  dirserver =%s   \npacking=%s #Ignore this",
		expect.userName,
		expect.keyserver,
		expect.storeserver,
		expect.dirserver,
		pack.Lookup(expect.packing).String())
}

func saveEnvs(e *envs) {
	e.name = os.Getenv("upspinname")
	e.keyserver = os.Getenv("upspinkeyserver")
	e.dirserver = os.Getenv("upspindirserver")
	e.storeserver = os.Getenv("upspinstoreserver")
	e.packing = os.Getenv("upspinpacking")
}

func restoreEnvs(e *envs) {
	os.Setenv("upspinname", e.name)
	os.Setenv("upspinkeyserver", e.keyserver)
	os.Setenv("upspindirserver", e.dirserver)
	os.Setenv("upspinstoreserver", e.storeserver)
	os.Setenv("upspinpacking", e.packing)
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
	context, err := InitContext(bytes.NewBufferString(config))
	if err != nil {
		t.Fatalf("could not parse config %s: %s", config, err)
	}
	if context.UserName() != expect.userName {
		t.Errorf("name: got %s expected %s", context.UserName(), expect.userName)
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
			t.Errorf("%d: got %s expected %s", i, test.got, test.expected)
		}
	}
	if context.Packing() != expect.packing {
		t.Errorf("got %s expected %s", context.Packing, expect.packing)
	}
}
