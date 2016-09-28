// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command archive creates an archive in tar format of an Upspin tree.
package main

// TODOs:
// - Better regexp matching (support sed-like behavior).
// - Keep time from original archive.
// - Add tests.
// - Add -v flag for verbose operation.

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
		fmt.Fprintf(os.Stderr, "too many args: %d", flag.NArg())
		usage()
	}
	if err != nil {
		exitf(err.Error())
	}
	os.Exit(0)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of archive:\n")
	fmt.Fprintf(os.Stderr, "\tarchive <upspin path> <os file path>\n")
	fmt.Fprintf(os.Stderr, "\t\tArchives an Upspin path into a local file.\n")
	fmt.Fprintf(os.Stderr, "\t\tE.g. archive user@domain.com/dir /tmp/foo\n")
	fmt.Fprintf(os.Stderr, "\n\tarchive <os file path>\n")
	fmt.Fprintf(os.Stderr, "\t\tUnarchives the contents of a local file into Upspin.\n")
	fmt.Fprintf(os.Stderr, "\t\tE.g. archive /tmp/foo\n")
	fmt.Fprintf(os.Stderr, "\t\tTo override the destination, use -match and -replace:\n")
	fmt.Fprintf(os.Stderr, "\t\tE.g. archive /tmp/foo -match user@domain.com -replace=newuser@a.uk\n")
	fmt.Fprintf(os.Stderr, "\nFlags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func newArchiver() (*archiver, error) {
	ctx, err := context.InitContext(nil)
	if err != nil {
		return nil, err
	}
	c := client.New(ctx)
	return &archiver{
		client:        c,
		prefixMatch:   *match,
		prefixReplace: *replace,
	}, nil
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
		// TODO: use -v
		fmt.Printf("Archiving %q\n", e.Name)
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
