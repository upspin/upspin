// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	gContext "context"
	"io/ioutil"
	"os"
	gPath "path"
	"path/filepath"

	"upspin.io/access"
	"upspin.io/auth/grpcauth"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// can reports whether the user associated with the given context has
// the given right to access the given path.
func can(s grpcauth.SecureServer, ctx gContext.Context, right access.Right, parsed path.Parsed) (bool, error) {
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		return false, err
	}

	a := defaultAccess
	afn, err := whichAccess(parsed)
	if err != nil {
		return false, err
	}
	if afn != "" {
		data, err := readFile(afn)
		if err != nil {
			return false, err
		}
		a, err = access.Parse(afn, data)
		if err != nil {
			return false, err
		}
	}

	return a.Can(session.User(), right, parsed.Path(), readFile)
}

// whichAccess is the core of the WhichAccess method,
// factored out so it can be called from other locations.
func whichAccess(parsed path.Parsed) (upspin.PathName, error) {
	// Look for Access file starting at end of local path.
	for i := 0; i <= parsed.NElem(); i++ {
		dir := filepath.Join(*root, filepath.FromSlash(parsed.Drop(i).FilePath()))
		if fi, err := os.Stat(dir); err != nil {
			return "", err
		} else if !fi.IsDir() {
			continue
		}
		name := filepath.Join(dir, "Access")
		fi, err := os.Stat(name)
		// Must exist and be a plain file.
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		// File exists. Is it a regular file?
		accessFile := gPath.Join(parsed.Drop(i).String(), "Access")
		if !fi.Mode().IsRegular() {
			return "", errors.Errorf("%q is not a regular file", accessFile)
		}
		fd, err := os.Open(name)
		if err != nil {
			// File exists but cannot be read.
			return "", err
		}
		fd.Close()
		return upspin.PathName(accessFile), nil

	}
	return "", nil
}

// readFile returns the contents of the named file relative to the server root.
// The file must be world-readable, or readFile returns a permissoin error.
func readFile(name upspin.PathName) ([]byte, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	localName := *root + parsed.FilePath()
	info, err := os.Stat(localName)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.E(errors.IsDir, name)
	}
	// Require world-readability on the local file system
	// to prevent accidental information leakage (e.g. $HOME/.ssh).
	// TODO(r,adg): find a less conservative policy for this.
	if info.Mode()&04 == 0 {
		return nil, errors.E(errors.Permission, errors.Str("not world-readable"), name)
	}

	// TODO(r, adg): think about symbolic links.
	return ioutil.ReadFile(localName)
}
