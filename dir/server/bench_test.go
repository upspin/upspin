// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// These benchmarks by default run on local storage. To isolate the performance
// of the DirServer from the system's storage the DirServer logs should be
// written to a ram disk.
//
// To set up a ram disk on Linux Ubuntu 16.04 do:
//
// sudo mkdir /mnt/ramdisk
// sudo chmod 777 /mnt/ramdisk
// sudo mount -t ramfs -o size=20m,user ramfs /mnt/ramdisk
//
// Then run benchmarks:
//
// go test -bench=. -args -tmpdir=/mnt/ramdisk
//

func BenchmarkPutAtRoot(b *testing.B) {
	benchmarkPut(userName, b)
}

func BenchmarkPut1Deep(b *testing.B) {
	benchmarkPut(userName+"/"+mkName(b), b)
}

func BenchmarkPut2Deep(b *testing.B) {
	benchmarkPut(userName+"/"+mkName(b)+"/"+mkName(b), b)
}

func BenchmarkPut4Deep(b *testing.B) {
	benchmarkPut(userName+"/"+mkName(b)+"/"+mkName(b)+"/"+mkName(b)+"/"+mkName(b), b)
}

func benchmarkPut(dir upspin.PathName, b *testing.B) {
	s, _, testDir := setupBenchServer(b)
	defer os.RemoveAll(testDir)

	p, err := path.Parse(dir)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < p.NElem(); i++ {
		n := p.First(i + 1)
		makeDirectory(s, n.Path())
	}
	for i := 0; i < b.N; i++ {
		subdir := mkName(b)
		name := dir + "/" + subdir
		_, err := s.Put(&upspin.DirEntry{
			Name:       name,
			SignedName: name,
			Attr:       upspin.AttrDirectory,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func setupBenchServer(t testing.TB) (*server, upspin.Config, string) {
	testDir, err := ioutil.TempDir(*tmpDir, "DirServer.Bench")
	if err != nil {
		panic(err)
	}
	generatorInstance = nil
	log.SetOutput(nil)
	s, cfg := newDirServerForTestingWithDir(t, userName, testDir)
	_, err = makeDirectory(s, userName+"/")
	if err != nil {
		t.Fatal(err)
	}
	return s, cfg, testDir
}

func mkName(b *testing.B) upspin.PathName {
	var buf [10]byte
	_, err := rand.Read(buf[:])
	if err != nil {
		b.Fatal(err)
	}
	return upspin.PathName(fmt.Sprintf("%x", buf))
}
