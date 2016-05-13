package main

import (
	"sync"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	numLocks = 100
)

var (
	mu    sync.Mutex
	locks []*sync.Mutex
)

// PathLock returns a mutex associated with a given path.
func PathLock(path upspin.PathName) *sync.Mutex {
	mu.Lock()
	defer mu.Unlock()

	lockNum := hashCode(string(path))
	lock := locks[lockNum%numLocks]

	return lock
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
