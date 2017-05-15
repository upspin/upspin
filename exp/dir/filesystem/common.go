// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is identical to upspin.io/store/filesystem/common.go
// Please keep them in sync.

package filesystem // import "upspin.io/exp/dir/filesystem"

import (
	"io/ioutil"
	"os"
	gPath "path"
	"path/filepath"
	"strings"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// newRoot parses the given options for the file system "root"
// and sets up a default access file.
// It is used by server constructors.
func newRoot(cfg upspin.Config, opts []string) (root string, defaultAccess *access.Access, err error) {
	for _, o := range opts {
		switch {
		case strings.HasPrefix(o, "root="):
			root = o[len("root="):]
		default:
			return "", nil, errors.E(errors.Invalid, errors.Errorf("bad option %q", o))
		}
	}

	if root == "" {
		return "", nil, errors.E(errors.Invalid, errors.Str("root must be set"))
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", nil, errors.E(errors.Invalid, errors.Str("root must be an absolute path"))
	}
	if fi, err := os.Stat(root); os.IsNotExist(err) {
		return "", nil, errors.E(errors.NotExist, err)
	} else if err != nil {
		return "", nil, errors.E(errors.IO, err)
	} else if !fi.IsDir() {
		return "", nil, errors.Str("root must be a directory")
	}

	defaultAccess, err = access.New(upspin.PathName(cfg.UserName()) + "/Access")
	if err != nil {
		return "", nil, err
	}

	return root, defaultAccess, nil
}

// can reports whether the user associated with the given config has
// the given right to access the given path.
func can(root string, defaultAccess *access.Access, user upspin.UserName, right access.Right, parsed path.Parsed) (bool, error) {
	a := defaultAccess
	afn, err := whichAccess(root, parsed)
	if err != nil {
		return false, err
	}
	if afn != "" {
		data, err := readFile(root, afn)
		if err != nil {
			return false, err
		}
		a, err = access.Parse(afn, data)
		if err != nil {
			return false, err
		}
	}
	return a.Can(user, right, parsed.Path(), func(name upspin.PathName) ([]byte, error) {
		return readFile(root, name)
	})
}

// whichAccess is the core of the WhichAccess method,
// factored out so it can be called from other locations.
func whichAccess(root string, parsed path.Parsed) (upspin.PathName, error) {
	// Look for Access file starting at end of local path.
	for i := 0; i <= parsed.NElem(); i++ {
		dir := filepath.Join(root, filepath.FromSlash(parsed.Drop(i).FilePath()))
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
func readFile(root string, name upspin.PathName) ([]byte, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	localName := filepath.Join(root, parsed.FilePath())
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
