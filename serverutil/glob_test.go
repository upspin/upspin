// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package serverutil

import (
	"strings"
	"testing"

	"upspin.io/errors"
	"upspin.io/upspin"
)

var (
	errLink       = upspin.ErrFollowLink
	errNotExist   = errors.E(errors.NotExist)
	errPermission = errors.E(errors.Permission)
)

func TestGlob(t *testing.T) {
	const (
		user        = "user@example.com"
		root        = user + "/"
		dir         = user + "/dir"
		file        = dir + "/file"
		link        = dir + "/link"
		linkTarget  = "user@example.org/somewhere"
		private     = dir + "/private"
		public      = dir + "/public"
		pubFile     = public + "/file"
		pubDir      = public + "/dir"
		pubDirFile  = pubDir + "/file"
		globDir     = public + "/gl[o]bdir"
		globDirFile = globDir + "/file"
	)
	lookup := func(name upspin.PathName) (de *upspin.DirEntry, err error) {
		defer func() {
			var deName upspin.PathName
			if de != nil {
				deName = de.Name
			}
			t.Logf("lookup(%q) = (%v, %v)", name, deName, err)
		}()
		de = &upspin.DirEntry{
			Name: name,
			Attr: upspin.AttrDirectory,
		}
		switch name {
		case root, dir, private, public, pubDir, globDir:
			return de, nil
		case file, pubFile, pubDirFile, globDirFile:
			de.Attr = upspin.AttrNone
			return de, nil
		case link:
			de.Attr = upspin.AttrLink
			de.Link = linkTarget
			return de, upspin.ErrFollowLink // This is what DirServer.Lookup does with a link.
		default:
			if strings.HasPrefix(string(name), link+"/") {
				return &upspin.DirEntry{
					Name: link,
					Attr: upspin.AttrLink,
					Link: linkTarget,
				}, errLink
			}
			return nil, errNotExist
		}
	}
	ls := func(name upspin.PathName) (des []*upspin.DirEntry, err error) {
		defer func() {
			var names []upspin.PathName
			for _, de := range des {
				names = append(names, de.Name)
			}
			t.Logf("ls(%q) = (%v, %v)", name, names, err)
		}()
		switch name {
		case root:
			return []*upspin.DirEntry{
				{
					Name: dir,
					Attr: upspin.AttrDirectory,
				},
			}, nil
		case dir:
			// Return the entries deliberately out-of-order to make
			// sure they are sorted.
			return []*upspin.DirEntry{
				{
					Name: private,
					Attr: upspin.AttrDirectory,
				},
				{
					Name: public,
					Attr: upspin.AttrDirectory,
				},
				{
					Name: link,
					Attr: upspin.AttrLink,
					Link: linkTarget,
				},
				{
					Name: file,
				},
			}, nil
		case private:
			return nil, errPermission
		case public:
			return []*upspin.DirEntry{
				{
					Name: pubDir,
					Attr: upspin.AttrDirectory,
				},
				{
					Name: pubFile,
				},
			}, nil
		case pubDir:
			return []*upspin.DirEntry{
				{
					Name: pubDirFile,
				},
			}, nil
		case globDir:
			return []*upspin.DirEntry{
				{
					Name: globDirFile,
				},
			}, nil
		default:
			if name == link || strings.HasPrefix(string(name), string(link+"/")) {
				return []*upspin.DirEntry{
					{
						Name: link,
						Link: link,
						Attr: upspin.AttrLink,
					},
				}, upspin.ErrFollowLink
			}
			return nil, errNotExist
		}
	}

	testGlob := func(pattern string, matchErr error, names ...upspin.PathName) {
		t.Logf("Glob(%q)", pattern)
		entries, err := Glob(pattern, lookup, ls)
		t.Log(entries, err)
		if err != matchErr && !errors.Match(matchErr, err) {
			t.Errorf("Glob(%q): error: %v, want %v", pattern, err, matchErr)
		}
		if err := matchEntries(entries, names...); err != nil {
			t.Errorf("Glob(%q): %v", pattern, err)
		}
	}

	testGlob(root, nil, root)
	testGlob(root+"*", nil, dir)
	testGlob(root+"/*", nil, dir) // double slash.
	testGlob(user+"/dir", nil, dir)
	testGlob(user+"/dir/*", nil, file, link, private, public)
	testGlob(user+"/dir/*/*", errLink, link, pubDir, pubFile)
	testGlob(user+"/dir/*/file", errLink, link, pubFile)
	testGlob(user+"/dir/p*/file", nil, pubFile)
	testGlob(user+"/dir/p*/*", nil, pubDir, pubFile)
	testGlob(user+"/dir/p*/*/*", nil, pubDirFile)
	testGlob(user+"/dir/private/*", errPermission)
	testGlob(user+"/dir/*/dir/*", errLink, link, pubDirFile)
	testGlob(link, nil, link)
	testGlob(link+"/*", errLink, link)
	testGlob(link+"/foo/*", errLink, link)
	testGlob(upspin.AllFilesGlob(globDir), nil, globDirFile)
}

func matchEntries(entries []*upspin.DirEntry, names ...upspin.PathName) error {
	if len(entries) != len(names) {
		return errors.Errorf("got %d entries, want %d", len(entries), len(names))
	}
	for i, e := range entries {
		if e.Name != names[i] {
			return errors.Errorf("entry %d named %q, want %q", i, e.Name, names[i])
		}
	}

	return nil
}

func TestHasMeta(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"foo*", true},
		{"fo?", true},
		{"foo", false},
		{"f\\*oo", false},
		{"f\\[o]o", false},
	}
	for _, c := range cases {
		got := hasMeta(c.in)
		if got != c.want {
			t.Errorf("hasMeta(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestUnquote(t *testing.T) {
	cases := []struct {
		in   string
		want upspin.PathName
	}{
		{"foo", "foo"},
		{"f[o]o", "f[o]o"},
		{"f\\[o]o", "f[o]o"},
		{"foo\\", "foo\\"},
	}
	for _, c := range cases {
		got := unquote(c.in)
		if got != c.want {
			t.Errorf("unquote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
