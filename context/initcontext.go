// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package context creates a client context from various sources.
package context

import (
	"bufio"
	"io"
	"os"
	"path"
	"strings"

	"upspin.io/endpoint"
	"upspin.io/errors"
	"upspin.io/key/keyloader"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/upspin"
)

// InitContext returns a context generated from a configuration file and/or
// environment variables.
//
// The default configuration file location is $HOME/upspin/rc.
// If passed a non-nil io.Reader, that is used instead of the default file.
// The upspinuser, upspindirectory, upspinstore, and upspinpacking environment
// variables specify the user, directory, store, and packing, and will override
// values in the provided reader or default rc file.
//
// Any endpoints not set in the data for the context will be set to the
// "unassigned" transport and an empty network address.
//
// A configuration file should be of the format
//   # lines that begin with a hash are ignored
//   key = value
// where key may be one of user, directory, store, or packing.
//
func InitContext(r io.Reader) (*upspin.Context, error) {
	const op = "InitContext"
	vals := map[string]string{
		"name":      "noone@nowhere.org",
		"user":      "",
		"directory": "",
		"store":     "",
		"packing":   "plain"}

	if r == nil {
		home := os.Getenv("HOME")
		if len(home) == 0 {
			log.Fatal("no home directory")
		}
		if f, err := os.Open(path.Join(home, "upspin/rc")); err == nil {
			r = f
			defer f.Close()
		}
	}

	// First source of truth is the RC file.
	if r != nil {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			// Remove comments.
			if sharp := strings.IndexByte(line, '#'); sharp >= 0 {
				line = line[:sharp]
			}
			line = strings.TrimSpace(line)
			tokens := strings.SplitN(line, "=", 2)
			if len(tokens) != 2 {
				continue
			}
			val := strings.TrimSpace(tokens[1])
			attr := strings.TrimSpace(tokens[0])
			if _, ok := vals[attr]; ok {
				vals[attr] = val
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	// Environment variables trump the RC file.
	for k := range vals {
		if v := os.Getenv("upspin" + k); len(v) != 0 {
			vals[k] = v
		}
	}

	context := new(upspin.Context)
	context.UserName = upspin.UserName(vals["name"])
	packer := pack.LookupByName(vals["packing"])
	if packer == nil {
		return nil, errors.Errorf("unknown packing %s", vals["packing"])
	}
	context.Packing = packer.Packing()

	// Implicitly load the user's keys from $HOME/.ssh.
	// This must be done before bind so that keys are ready for authenticating to servers.
	// TODO(edpin): fix this by re-checking keys when they're needed.
	// TODO(ehg): remove loading of private key
	var err error
	err = keyloader.Load(context)
	if err != nil {
		log.Error.Print(err)
		return nil, err
	}

	context.UserEndpoint = parseEndpoint(op, vals, "user", &err)
	context.StoreEndpoint = parseEndpoint(op, vals, "store", &err)
	context.DirectoryEndpoint = parseEndpoint(op, vals, "directory", &err)
	return context, err
}

var ep0 upspin.Endpoint // Will have upspin.Unassigned as transport.

func parseEndpoint(op string, vals map[string]string, name string, errorp *error) upspin.Endpoint {
	text, ok := vals[name]
	if !ok || text == "" {
		// No setting for this value, so set to 'unassigned'.
		return ep0
	}
	ep, err := endpoint.Parse(text)
	if err != nil {
		log.Error.Printf("%s: cannot parse %q service: %s", op, text, err)
		if *errorp == nil {
			*errorp = err
		}
		return ep0
	}
	return *ep
}
