package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"upspin.io/flags"
	"upspin.io/key/sha256key"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

func (s *State) validate(args ...string) {
	const help = `
Validate checks whether the file in upspin matches your local copy.
It reports back the differences, files that do not exist in upspin,
incomplete files in upspin, and files with different sha256 sums.

The -recursive flag can be added to recur through sub-directories.`

	fs := flag.NewFlagSet("put", flag.ExitOnError)
	recur := fs.Bool("recursive", false, "recur through sub-directories (default false)")
	s.ParseFlags(fs, args, help, "validate [-recursive] <upspinpath> <localpath>")

	if fs.NArg() != 2 {
		usageAndExit(fs)
	}
	pat := s.AtSign(fs.Arg(0))
	parsed, err := path.Parse(pat)
	if err != nil {
		s.Exit(err)
	}

	entry, err := s.Client.Lookup(parsed.Path(), true)
	if err != nil {
		s.Exit(err)
	}

	var localIsDir bool
	if fi, err := os.Stat(fs.Arg(1)); err != nil {
		s.Exit(err)
	} else {
		localIsDir = fi.IsDir()
	}

	if entry.IsDir() != localIsDir {
		s.Exitf("comparing a file with a directory %s, %s", entry.Name, fs.Arg(1))
	}

	seen := map[string]bool{}
	done := map[upspin.PathName]bool{}
	s.val(entry, fs.Arg(1), localIsDir, seen, done, *recur)
	var missing bool
	for path, inUpspin := range seen {
		if !inUpspin {
			if !missing {
				fmt.Println("files missing in upspin:")
				missing = true
			}
			fmt.Println(path)
		}
	}
}

func (s *State) val(
	entry *upspin.DirEntry,
	localPath string,
	localIsDir bool,
	seen map[string]bool,
	done map[upspin.PathName]bool,
	recur bool,
) {
	done[entry.Name] = true

	var dirContents []*upspin.DirEntry
	if entry.IsDir() {
		var err error
		dirContents, err = s.Client.Glob(upspin.AllFilesGlob(entry.Name))
		if err != nil {
			s.Exit(err)
		}
	} else {
		dirContents = []*upspin.DirEntry{entry}
	}

	if err := filepath.Walk(localPath, func(curPath string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println(err)
			return err
		}
		if curPath == localPath {
			return nil
		}
		if info.IsDir() {
			seen[curPath] = false
			return filepath.SkipDir
		}
		seen[curPath] = false
		return nil
	}); err != nil {
		s.Exit(err)
	}

	for _, entry := range dirContents {
		if localIsDir {
			file, err := appendBase(entry, localPath)
			if err != nil {
				s.Exit(err)
			}
			s.doValidate(entry, file, seen)
		} else {
			s.doValidate(entry, localPath, seen)
		}
	}

	if !recur {
		return
	}
	for _, entry := range dirContents {
		if entry.IsDir() && !done[entry.Name] {
			file, err := appendBase(entry, localPath)
			if err != nil {
				s.Exit(err)
			}
			s.val(entry, file, true, seen, done, recur)
		}
	}
}

func appendBase(entry *upspin.DirEntry, localPath string) (string, error) {
	var base string
	if p, err := path.Parse(entry.Name); err != nil {
		return "", err
	} else {
		base = filepath.Base(p.FilePath())
	}
	return filepath.Join(localPath, base), nil
}

func (s *State) doValidate(entry *upspin.DirEntry, path string, seen map[string]bool) {
	if entry.IsDir() {
		fi, err := os.Stat(path)
		if err != nil {
			s.Exit(err)
		}
		if fi.IsDir() {
			seen[path] = true
		}
		return
	}
	localEntry := s.packLocalFile(entry, path)
	if localEntry == nil {
		fmt.Printf("skipping %s\n", entry.Name)
		return
	}
	if !blocksEqual(entry, localEntry) {
		fmt.Printf("%s does not match local file %s\n", entry.Name, path)
		return
	}
	seen[path] = true
}

func (s *State) packLocalFile(entry *upspin.DirEntry, path string) *upspin.DirEntry {
	if entry.Writer != s.Config.UserName() {
		fmt.Printf(
			"%s can not pack data from different writers %s, %s\n",
			entry.Name,
			entry.Writer,
			s.Config.UserName(),
		)
		return nil
	}

	packer := pack.Lookup(entry.Packing)
	if packer == nil {
		s.Exitf("unrecognized Packing %d", entry.Packing)
	}
	localEntry := &upspin.DirEntry{
		Name:       entry.Name,
		SignedName: entry.Name,
		Packing:    entry.Packing,
		Time:       entry.Time,
		Sequence:   upspin.SeqIgnore,
		Writer:     entry.Writer,
		Link:       "",
		Attr:       upspin.AttrNone,
		Packdata:   entry.Packdata,
	}
	bp, err := packer.Pack(s.Config, localEntry)
	if err != nil {
		s.Exit(err)
	}
	data := s.ReadAll(path)
	for len(data) > 0 {
		n := len(data)
		if n > flags.BlockSize {
			n = flags.BlockSize
		}
		cipher, err := bp.Pack(data[:n])
		if err != nil {
			s.Exit(err)
		}
		data = data[n:]
		bp.SetLocation(upspin.Location{
			Endpoint:  s.Config.StoreEndpoint(),
			Reference: upspin.Reference(sha256key.Of(cipher).String()),
		})
	}
	bp.Close()
	return localEntry
}

func blocksEqual(a, b *upspin.DirEntry) bool {
	if len(a.Blocks) != len(b.Blocks) {
		return false
	}
	for i := range a.Blocks {
		if !bytes.Equal(a.Blocks[i].Packdata, b.Blocks[i].Packdata) {
			fmt.Printf("    %x\n    %x\n", a.Blocks[i].Packdata, b.Blocks[i].Packdata)
			return false
		}
	}
	return true
}
