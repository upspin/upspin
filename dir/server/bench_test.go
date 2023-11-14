// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"fmt"
	"os"
	"testing"

	"upspin.io/cache"
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
// mkdir /dev/shm/benchdir
//
// Then run benchmarks:
//
// env TMPDIR=/dev/shm/benchdir go test -bench=. -benchmem
//

func BenchmarkPutAtRoot(b *testing.B) {
	benchmarkPut(b, userName)
}

func BenchmarkPut1Deep(b *testing.B) {
	benchmarkPut(b, userName+"/"+mkName())
}

func BenchmarkPut2Deep(b *testing.B) {
	benchmarkPut(b, userName+"/"+mkName()+"/"+mkName())
}

func BenchmarkPut4Deep(b *testing.B) {
	benchmarkPut(b, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName())
}

func benchmarkPut(b *testing.B, dir upspin.PathName) {
	b.StopTimer()
	s, _, cleanup := setupBenchServer(b)
	defer cleanup()
	mkAll(b, s, dir)
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		subdir := mkName()
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

const cached = true

func BenchmarkLookupAtRootNotCached(b *testing.B) {
	benckmarkLookup(b, !cached, userName+"/"+mkName())
}

func BenchmarkLookupAtRootCached(b *testing.B) {
	benckmarkLookup(b, cached, userName+"/"+mkName())
}

func BenchmarkLookup4DeepNotCached(b *testing.B) {
	benckmarkLookup(b, !cached, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName())
}

func BenchmarkLookup4DeepCached(b *testing.B) {
	benckmarkLookup(b, cached, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName())
}

func benckmarkLookup(b *testing.B, cached bool, dir upspin.PathName) {
	b.StopTimer()
	s, _, cleanup := setupBenchServer(b)
	defer cleanup()
	s.userTrees = cache.NewLRU(1)
	mkAll(b, s, dir)
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		_, err := s.Lookup(dir)
		if err != nil {
			b.Fatal(err)
		}
		if !cached {
			s.userTrees.RemoveOldest()
		}
	}
}

func BenchmarkDeleteAtRoot(b *testing.B) {
	benchmarkDelete(b, userName)
}

func BenchmarkDelete1Deep(b *testing.B) {
	benchmarkDelete(b, userName+"/"+mkName())
}

func BenchmarkDelete2Deep(b *testing.B) {
	benchmarkDelete(b, userName+"/"+mkName()+"/"+mkName())
}

func BenchmarkDelete4Deep(b *testing.B) {
	benchmarkDelete(b, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName())
}

func benchmarkDelete(b *testing.B, dir upspin.PathName) {
	b.StopTimer()
	s, _, cleanup := setupBenchServer(b)
	defer cleanup()
	mkAll(b, s, dir)

	var names []upspin.PathName
	for i := 0; i < b.N; i++ {
		subdir := mkName()
		name := dir + "/" + subdir
		_, err := s.Put(&upspin.DirEntry{
			Name:       name,
			SignedName: name,
			Attr:       upspin.AttrDirectory,
		})
		if err != nil {
			b.Fatal(err)
		}
		names = append(names, name)
	}

	b.StartTimer()
	for _, name := range names {
		_, err := s.Delete(name)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmarks for WhichAccess have two parameters besides the caching or no
// caching one: 1) the directory that contains the Access file and 2) how far
// under that directory the path name we give to WhichAccess.
func BenchmarkWhichAccessNoCacheRoot(b *testing.B) {
	benchmarkWhichAccess(b, !cached, userName, 0)
}
func BenchmarkWhichAccessCacheRoot(b *testing.B) {
	benchmarkWhichAccess(b, cached, userName, 0)
}
func BenchmarkWhichAccessNoCache1Deep0Dist(b *testing.B) {
	benchmarkWhichAccess(b, !cached, userName+"/"+mkName(), 0)
}
func BenchmarkWhichAccessCache1Deep0Dist(b *testing.B) {
	benchmarkWhichAccess(b, cached, userName+"/"+mkName(), 0)
}
func BenchmarkWhichAccessNoCache1Deep1Dist(b *testing.B) {
	benchmarkWhichAccess(b, !cached, userName+"/"+mkName(), 1)
}
func BenchmarkWhichAccessCache1Deep1Dist(b *testing.B) {
	benchmarkWhichAccess(b, cached, userName+"/"+mkName(), 1)
}
func BenchmarkWhichAccessNoCache4Deep3Dist(b *testing.B) {
	benchmarkWhichAccess(b, !cached, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName(), 3)
}
func BenchmarkWhichAccessCache4Deep3Dist(b *testing.B) {
	benchmarkWhichAccess(b, cached, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName(), 3)
}
func BenchmarkWhichAccessNoCache4Deep10Dist(b *testing.B) {
	benchmarkWhichAccess(b, !cached, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName(), 10)
}
func BenchmarkWhichAccessCache4Deep10Dist(b *testing.B) {
	benchmarkWhichAccess(b, cached, userName+"/"+mkName()+"/"+mkName()+"/"+mkName()+"/"+mkName(), 10)
}

func benchmarkWhichAccess(b *testing.B, cached bool, dir upspin.PathName, accessDistance int) {
	b.StopTimer()
	s, cfg, cleanup := setupBenchServer(b)
	defer cleanup()
	s.userTrees = cache.NewLRU(1)
	s.access = cache.NewLRU(1)
	s.defaultAccess = cache.NewLRU(1)
	putAccessOrGroupFile(b, s, cfg, dir+"/Access", "*:"+userName)
	for i := 0; i < accessDistance; i++ {
		dir = dir + "/" + mkName()
		makeDirectory(s, dir)
	}
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		_, err := s.WhichAccess(dir + "/somename")
		if err != nil {
			b.Fatal(err)
		}
		if !cached {
			b.StopTimer()
			s.userTrees.RemoveOldest()
			s.access.RemoveOldest()
			s.defaultAccess.RemoveOldest()
			b.StartTimer()
		}
	}
}

// setupBenchServer sets up the benchmark tests and returns the server to use,
// the user's config and a clean up function to use after benchmarks are run.
func setupBenchServer(t testing.TB) (*server, upspin.Config, func()) {
	testDir, err := os.MkdirTemp("", "DirServer.Bench")
	if err != nil {
		panic(err)
	}
	generatorInstance = nil
	log.SetOutput(nil)
	s, cfg := newDirServerForTestingWithTestDir(t, userName, testDir)
	_, err = makeDirectory(s, userName+"/")
	if err != nil {
		t.Fatal(err)
	}
	f := func() {
		os.RemoveAll(testDir)
		log.SetOutput(os.Stderr)
	}
	return s, cfg, f
}

func mkAll(b *testing.B, s *server, dir upspin.PathName) {
	p, err := path.Parse(dir)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < p.NElem(); i++ {
		makeDirectory(s, p.First(i+1).Path())
	}
}

var nameCount int

func mkName() upspin.PathName {
	nameCount++
	return upspin.PathName(fmt.Sprintf("%d", nameCount))
}
