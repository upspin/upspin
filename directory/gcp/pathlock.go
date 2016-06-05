// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"sync"

	"upspin.io/upspin"
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
