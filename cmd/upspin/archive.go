// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command archive creates an archive in tar format of an Upspin tree.
package main

// TODOs:
// - Better regexp matching (support sed-like behavior).
// - Keep time from original archive.
// - Add tests.
// - Integrate with cp logic.

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// archiver implements archiving and unarchiving to/from Upspin tree and a local
// file system.
type archiver struct {
	// client is the Upspin client to use for read or write.
	client upspin.Client

	// prefixMatch and prefixReplace are used when unarchiving from an
	// archive when the destination path should be matched and replaced.
	// See flags match and replace.
	prefixMatch   string
	prefixReplace string

	verbose bool
}

func (s *State) tar(args ...string) {
	const help = `
Tar archives an Upspin tree into a local tar file.
E.g. tar user@domain.com/dir /tmp/archive.tar
`
	fs := flag.NewFlagSet("tar", flag.ExitOnError)
	verbose := fs.Bool("v", false, "verbose output")
	s.parseFlags(fs, args, help, "tar upspin_directory local_file")
	if fs.NArg() != 2 {
		fs.Usage()
	}
	a, err := s.newArchiver(*verbose)
	if err != nil {
		s.exitf(err.Error())
	}
	err = a.archive(upspin.PathName(fs.Arg(0)), s.createOrDie(fs.Arg(1)))
	if err != nil {
		s.exitf(err.Error())
	}
}

func (s *State) untar(args ...string) {
	const help = `
Untar unarchives a local tar file into Upspin.
E.g. untar /tmp/archive.tar

Untar allows the prefix of the Upspin path contained in the tar file to
be replaced. The destination Upspin prefix must exist. For example:

   untar /tmp/archive.tar -match bob@a.com -replace sue@a.com

untars archive.tar replacing the paths
`
	fs := flag.NewFlagSet("untar", flag.ExitOnError)
	match := fs.String("match", "", "if present, matches pathname prefixes")
	replace := fs.String("replace", "", "if present, replaces the pathname matched by flag -match")
	verbose := fs.Bool("v", false, "verbose output")

	s.parseFlags(fs, args, help, "untar local_file")
	if fs.NArg() != 1 {
		fs.Usage()
	}
	a, err := s.newArchiver(*verbose)
	if err != nil {
		s.exitf(err.Error())
	}
	a.matchReplace(*match, *replace)
	err = a.unarchive(s.openOrDie(fs.Arg(0)))
	if err != nil {
		s.exitf(err.Error())
	}
}

func (s *State) newArchiver(verbose bool) (*archiver, error) {
	return &archiver{
		client:  s.client,
		verbose: verbose,
	}, nil
}

func (a *archiver) matchReplace(match, replace string) {
	a.prefixMatch = match
	a.prefixReplace = replace
}

// archive walks the pathName and writes the contents to dst.
func (a *archiver) archive(pathName upspin.PathName, dst io.WriteCloser) error {
	tw := tar.NewWriter(dst)

	if err := a.doArchive(pathName, tw, dst); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return dst.Close()
}

// doArchive is called by the archive method to walk subdirectories.
func (a *archiver) doArchive(pathName upspin.PathName, tw *tar.Writer, dst io.Writer) error {
	entries, err := a.client.Glob(string(path.Join(pathName, "*")))
	if err != nil {
		return err
	}
	for _, e := range entries {
		hdr := &tar.Header{
			Name:    string(e.Name),
			Mode:    0600,
			ModTime: e.Time.Go(),
		}
		if a.verbose {
			fmt.Printf("Archiving %q\n", e.Name)
		}
		switch {
		case e.IsDir():
			hdr.Typeflag = tar.TypeDir
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			// Recurse into this subdir.
			err = a.doArchive(e.Name, tw, dst)
			if err != nil {
				return err
			}
		case e.IsLink():
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = string(e.Link)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		default:
			size, err := e.Size()
			if err != nil {
				return err
			}
			hdr.Typeflag = tar.TypeReg
			hdr.Size = size
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if data, err := a.client.Get(e.Name); err != nil {
				return err
			} else if _, err := tw.Write(data); err != nil {
				return err
			}
		}
	}
	return nil
}

// unarchive reads an archive from src and restores it to its final location.
func (a *archiver) unarchive(src io.ReadCloser) error {
	defer src.Close()
	tr := tar.NewReader(src)

	// accessFiles keeps track of Access files' names and contents, since they're
	// unarchived last.
	type accessFiles struct {
		name     upspin.PathName
		contents []byte
	}

	var acc []accessFiles
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		var name upspin.PathName
		// Adjust names if necessary.
		if a.prefixMatch != "" {
			idx := strings.Index(hdr.Name, a.prefixMatch)
			if idx == 0 {
				// Must be an exact prefix.
				// TODO: support a more general sed-like behavior?
				name = upspin.PathName(a.prefixReplace + hdr.Name[idx+len(a.prefixMatch):])
			} else {
				// Skip if it doesn't match.
				continue
			}
		} else {
			name = upspin.PathName(hdr.Name)
		}

		if a.verbose {
			fmt.Fprintf(os.Stdout, "Extracting %q into %q\n", hdr.Name, name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			_, err = a.client.MakeDirectory(name)
			if err != nil && !errors.Match(errors.E(errors.Exist), err) {
				return err
			}
		case tar.TypeSymlink:
			_, err = a.client.PutLink(upspin.PathName(hdr.Linkname), name)
			if err != nil {
				return err
			}
		case tar.TypeReg:
			buf, err := ioutil.ReadAll(tr)
			if err != nil {
				return err
			}
			name := upspin.PathName(name)
			if access.IsAccessFile(name) {
				// Save Access files for later, to prevent
				// being locked out from restoring sub-entries.
				acc = append(acc, accessFiles{
					name:     name,
					contents: buf,
				})
				continue
			}
			_, err = a.client.Put(name, buf)
			if err != nil {
				return err
			}
		}
	}

	// Now extracts Access files.
	for _, af := range acc {
		_, err := a.client.Put(af.name, af.contents)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *State) openOrDie(path string) *os.File {
	f, err := os.Open(path)
	if err != nil {
		s.exitf(err.Error())
	}
	return f
}

func (s *State) createOrDie(path string) *os.File {
	f, err := os.Create(path)
	if err != nil {
		s.exitf(err.Error())
	}
	return f
}
