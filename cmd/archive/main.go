// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command archive creates an archive in tar format of an Upspin tree.
package main

// TODOs:
// - Better regexp matching (support sed-like behavior).
// - Keep time from original archive.
// - Add tests.

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"upspin.io/access"
	"upspin.io/client"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"

	// Load useful packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"

	// Load required transports
	_ "upspin.io/dir/transports"
	_ "upspin.io/key/transports"
	_ "upspin.io/store/transports"
)

var (
	match   = flag.String("match", "", "if present, matches pathname prefixes")
	replace = flag.String("replace", "", "if present, replaces the pathname matched by flag -match")
)

type archiver struct {
	// client is the Upspin client to use for read or write.
	client upspin.Client

	// prefixMatch and prefixReplace are used when unarchiving from an
	// archive when the destination path should be matched and replaced.
	// See flags match and replace.
	prefixMatch   string
	prefixReplace string
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

// doArchive is called by archive on sub-directories.
func (a *archiver) doArchive(pathName upspin.PathName, tw *tar.Writer, dst io.Writer) error {
	entries, err := a.client.Glob(string(path.Join(pathName, "*")))
	if err != nil {
		return err
	}
	for _, e := range entries {
		fmt.Printf("Archiving %q\n", e.Name)
		if e.IsDir() {
			hdr := &tar.Header{
				Name:     string(e.Name),
				Mode:     0600,
				Typeflag: tar.TypeDir,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			// Recurse into this subdir.
			err = a.doArchive(e.Name, tw, dst)
			if err != nil {
				return err
			}
		} else if e.IsLink() {
			hdr := &tar.Header{
				Name:     string(e.Name),
				Mode:     0600,
				Typeflag: tar.TypeSymlink,
				Linkname: string(e.Link),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		} else {
			size, err := e.Size()
			if err != nil {
				return err
			}
			hdr := &tar.Header{
				Name:     string(e.Name),
				Mode:     0600,
				Typeflag: tar.TypeReg,
				Size:     size,
			}
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

	// file keeps track of file names and their contents
	type file struct {
		name     upspin.PathName
		contents []byte
	}

	var acc []file
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

		fmt.Fprintf(os.Stdout, "Extracting %q into %q\n", hdr.Name, name)

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
				acc = append(acc, file{
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

func newArchiver() (*archiver, error) {
	ctx, err := context.InitContext(nil)
	if err != nil {
		return nil, err
	}
	return &archiver{
		client:        client.New(ctx),
		prefixMatch:   *match,
		prefixReplace: *replace,
	}, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage of archive:
  archive <upspin path> <os file path>
      Archives an Upspin path into a local file.
      E.g. archive user@domain.com/dir /tmp/foo

  archive <os file path>
      Unarchives the contents of a local file into Upspin.
      E.g. archive /tmp/foo
      To override the destination, use -match and -replace.
      E.g. archive /tmp/foo -match user@domain.com -replace=newuser@a.uk

  Flags:
`)
	flag.PrintDefaults()
	os.Exit(2)
}

func exitf(reason string, params ...interface{}) {
	fmt.Fprintf(os.Stderr, reason, params...)
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(3)
}

func openOrDie(path string) *os.File {
	f, err := os.Open(path)
	if err != nil {
		exitf(err.Error())
	}
	return f
}

func createOrDie(path string) *os.File {
	f, err := os.Create(path)
	if err != nil {
		exitf(err.Error())
	}
	return f
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() < 1 {
		usage()
	}
	a, err := newArchiver()
	if err != nil {
		exitf(err.Error())
	}
	if flag.NArg() == 1 {
		err = a.unarchive(openOrDie(flag.Arg(0)))
	} else if flag.NArg() == 2 {
		err = a.archive(upspin.PathName(flag.Arg(0)), createOrDie(flag.Arg(1)))
	} else {
		exitf("too many args: %d", flag.NArg())
	}
	if err != nil {
		exitf(err.Error())
	}
	os.Exit(0)
}
