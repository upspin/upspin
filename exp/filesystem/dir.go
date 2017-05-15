// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filesystem

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

// DirServer returns the DirServer implementation for this Server.
func (s *Server) DirServer() upspin.DirServer {
	return &dirServer{s}
}

type dirServer struct {
	*Server
}

func (s dirServer) Dial(cfg upspin.Config, e upspin.Endpoint) (upspin.Service, error) {
	const op = "store/filesystem.Dial"

	dialed := *s.Server
	dialed.user = cfg
	return dirServer{&dialed}, nil
}

// verifyUserRoot checks that the user name in the path is the owner of this root.
func (s *Server) verifyUserRoot(parsed path.Parsed) error {
	if parsed.User() != s.server.UserName() {
		return errors.E(errors.Invalid, parsed.Path(), errors.Errorf("mismatched user name %q", parsed.User()))
	}
	return nil
}

func (s dirServer) Lookup(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/filesystem.Lookup"
	log.Println(op, pathName)

	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if err := s.verifyUserRoot(parsed); err != nil {
		return nil, errors.E(op, err)
	}
	if ok, err := can(s.root, s.defaultAccess, s.user.UserName(), access.List, parsed); err != nil {
		return nil, errors.E(op, err)
	} else if !ok {
		return nil, errors.E(op, access.ErrPermissionDenied)
	}
	e, err := s.entry(filepath.Join(s.root, parsed.FilePath()))
	if err != nil {
		return nil, errors.E(op, err)
	}
	return e, nil
}

// entry returns the DirEntry for the named local file or directory.
func (s dirServer) entry(file string) (*upspin.DirEntry, error) {
	// TODO(adg): handle symbolic links
	if !strings.HasPrefix(file, s.root) {
		return nil, errors.Str("internal error: not in root")
	}

	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	modTime := upspin.TimeFromGo(info.ModTime())

	attr := upspin.AttrNone
	if info.IsDir() {
		attr = upspin.AttrDirectory
	} else {
		// If this is a file we may have a cached DirEntry for it.
		v, ok := s.dirEntries.Get(file)
		if ok {
			entry := v.(*upspin.DirEntry)
			if entry.Time == modTime {
				return entry, nil
			}
			s.dirEntries.Remove(file)
		}
	}

	name := s.upspinPathFromLocal(file)
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Packing:    packing,
		Time:       modTime,
		Attr:       attr,
		Sequence:   0,
		Writer:     s.server.UserName(), // TODO: Is there a better answer?
	}
	if info.IsDir() {
		// Nothing left to do.
		return entry, nil
	}

	p := pack.Lookup(packing)
	bp, err := p.Pack(s.server, entry)
	if err != nil {
		return nil, err
	}
	contents, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	// Ignore the returned "ciphertext", as using the plain packer
	// it is equivalent to the cleartext.
	_, err = bp.Pack(contents)
	if err != nil {
		return nil, err
	}
	bp.SetLocation(upspin.Location{
		Endpoint:  s.server.StoreEndpoint(),
		Reference: upspin.Reference(file[len(s.root):]),
	})
	if err := bp.Close(); err != nil {
		return nil, err
	}

	s.dirEntries.Add(file, entry)
	return entry, nil
}

// upspinPathFromLocal returns the upspin.PathName for
// the given absolute local path name.
func (s *Server) upspinPathFromLocal(local string) upspin.PathName {
	return upspin.PathName(s.server.UserName()) + "/" + upspin.PathName(local[len(s.root):])
}

func (s dirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	const op = "dir/filesystem.Glob"
	log.Println(op, pattern)

	parsed, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		return nil, errors.E(op, err)
	}
	if err := s.verifyUserRoot(parsed); err != nil {
		return nil, errors.E(op, err)
	}

	var (
		matches []string
		next    = []string{s.root}
	)
	for i := 0; i < parsed.NElem(); i++ {
		elem := parsed.Elem(i)
		matches, next = next, matches[:0]
		for _, match := range matches {
			if isGlobPattern(elem) || i == parsed.NElem()-1 {
				parsed, err := path.Parse(s.upspinPathFromLocal(match))
				if err != nil {
					return nil, errors.E(op, err)
				}
				if ok, err := can(s.root, s.defaultAccess, s.user.UserName(), access.List, parsed); err != nil {
					return nil, errors.E(op, err)
				} else if !ok {
					continue
				}
			}
			names, err := filepath.Glob(filepath.Join(match, elem))
			// TODO(r): remove this error check
			if err != nil {
				return nil, errors.E(op, err)
			}
			next = append(next, names...)
		}
	}
	matches = next

	var entries []*upspin.DirEntry
	for _, match := range matches {
		e, err := s.entry(match)
		if err != nil {
			return nil, errors.E(op, err)
		}
		parsed, err := path.Parse(upspin.PathName(s.upspinPathFromLocal(match)))
		if err != nil {
			return nil, errors.E(op, err)
		}
		if ok, err := can(s.root, s.defaultAccess, s.user.UserName(), access.Read, parsed); err != nil {
			return nil, errors.E(op, err)
		} else if !ok {
			e.Blocks = nil
			e.Packdata = nil
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// isGlobPattern replies whether the given path element
// contains a glob pattern.
func isGlobPattern(elem string) bool {
	return strings.ContainsAny(elem, `*?[]`)
}

func (s dirServer) WhichAccess(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/filesystem.WhichAccess"
	log.Println(op, pathName)

	parsed, err := path.Parse(pathName)
	if err != nil {
		return nil, errors.E(op, err)
	}
	err = s.verifyUserRoot(parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	if ok, err := can(s.root, s.defaultAccess, s.user.UserName(), access.AnyRight, parsed); err != nil {
		return nil, errors.E(op, err)
	} else if !ok {
		return nil, errors.E(op, access.ErrPermissionDenied)
	}
	accessPath, err := whichAccess(s.root, parsed)
	if err != nil {
		return nil, errors.E(op, err)
	}
	e, err := s.entry(string(accessPath))
	if err != nil {
		return nil, errors.E(op, err)
	}
	return e, nil
}

// Watch implements upspin.DirServer.
func (d dirServer) Watch(upspin.PathName, int64, <-chan struct{}) (<-chan upspin.Event, error) {
	return nil, upspin.ErrNotSupported
}

// Methods that are not implemented.

var errReadOnly = errors.Str("read-only name space")

func (s dirServer) Delete(pathName upspin.PathName) (*upspin.DirEntry, error) {
	const op = "dir/filesystem.Delete"
	return nil, errors.E(op, errReadOnly)
}

func (s dirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	const op = "dir/filesystem.Put"
	return nil, errors.E(op, errReadOnly)
}
