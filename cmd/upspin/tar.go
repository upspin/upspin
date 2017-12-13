// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// TODOs:
// - Better regexp matching (support sed-like behavior).
// - Keep time from original archive.
// - Add tests.
// - Integrate with cp logic.

import (
	"archive/tar"
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	"flag"

	"upspin.io/access"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/subcmd"
	"upspin.io/upspin"
)

func (s *State) tar(args ...string) {
	const help = `
Tar archives an Upspin tree into a local tar file, or with the
-extract flag, unpacks a local tar file into an Upspin tree.

When extracting, the -match and -replace flags cause the extracted
file to have any prefix that matches be replaced by substitute text.
Whether or not these flags are used, the destination path must
always be in Upspin.
`
	fs := flag.NewFlagSet("tar", flag.ExitOnError)
	extract := fs.Bool("extract", false, "extract from archive")
	match := fs.String("match", "", "extract from the archive only those pathnames that match the `prefix`")
	replace := fs.String("replace", "", "replace -match prefix with the replacement `text`")
	fs.Bool("v", false, "verbose output")
	s.ParseFlags(fs, args, help, "tar [-extract [-match prefix -replace substitution] ] upspin_directory local_file")
	if !*extract {
		if *match != "" || *replace != "" {
			usageAndExit(fs)
		}
		s.tarCommand(fs)
		return
	}
	s.untarCommand(fs)
}

// archiver implements archiving and unarchiving to/from Upspin tree and a local
// file system.
type archiver struct {
	// state holds the current upspin state.
	state *State

	// client is the Upspin client to use for read or write.
	client upspin.Client

	// prefixMatch and prefixReplace are used when unarchiving from an
	// archive when the destination path should be matched and replaced.
	// See flags match and replace.
	prefixMatch   string
	prefixReplace string

	verbose bool
}

func (s *State) tarCommand(fs *flag.FlagSet) {
	if fs.NArg() != 2 {
		usageAndExit(fs)
	}
	a, err := s.newArchiver(subcmd.BoolFlag(fs, "v"))
	if err != nil {
		s.Exit(err)
	}
	dir := s.GlobOneUpspinPath(fs.Arg(0))
	file := s.GlobOneLocal(fs.Arg(1))
	err = a.archive(dir, s.CreateLocal(file))
	if err != nil {
		s.Exit(err)
	}
}

func (s *State) untarCommand(fs *flag.FlagSet) {
	if fs.NArg() != 1 {
		usageAndExit(fs)
	}
	a, err := s.newArchiver(subcmd.BoolFlag(fs, "v"))
	if err != nil {
		s.Exit(err)
	}
	a.matchReplace(subcmd.StringFlag(fs, "match"), subcmd.StringFlag(fs, "replace"))
	err = a.unarchive(s.OpenLocal(s.GlobOneLocal(fs.Arg(0))))
	if err != nil {
		s.Exit(err)
	}
}

func (s *State) newArchiver(verbose bool) (*archiver, error) {
	return &archiver{
		state:   s,
		client:  s.Client,
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
			fmt.Fprintf(a.state.Stderr, "Archiving %q\n", e.Name)
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
			f, err := a.client.Open(e.Name)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			if err != nil {
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
			fmt.Fprintf(a.state.Stderr, "Extracting %q into %q\n", hdr.Name, name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			_, err = a.client.MakeDirectory(name)
			if err != nil && !errors.Is(errors.Exist, err) {
				return err
			}
		case tar.TypeSymlink:
			_, err = a.client.PutLink(upspin.PathName(hdr.Linkname), name)
			if err != nil {
				return err
			}
		case tar.TypeReg:
			if access.IsAccessFile(name) {
				// Save Access files for later, to prevent
				// being locked out from restoring sub-entries.
				buf, err := ioutil.ReadAll(tr)
				if err != nil {
					return err
				}
				acc = append(acc, accessFiles{
					name:     name,
					contents: buf,
				})
				continue
			}
			f, err := a.client.Create(name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				a.client.Delete(name)
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}

	// Now extract Access files.
	for _, af := range acc {
		_, err := a.client.Put(af.name, af.contents)
		if err != nil {
			return err
		}
	}

	return nil
}
