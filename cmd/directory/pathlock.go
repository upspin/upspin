package main

import (
	"sync"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	numLocks = 100
)

var locks []*sync.Mutex

// pathLock returns a mutex associated with a given path.
func pathLock(path upspin.PathName) *sync.Mutex {
	lockNum := hashCode(string(path))
	return locks[lockNum%numLocks]
}

func hashCode(s string) uint64 {
	h := uint64(0)
	for _, c := range s {
		h = 31*h + uint64(c)
	}
	return h
}

func init() {
	locks = make([]*sync.Mutex, numLocks)
	for i := range locks {
		locks[i] = new(sync.Mutex)
	}
}
