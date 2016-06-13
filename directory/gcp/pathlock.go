// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"sync"

	"upspin.io/upspin"
)

// A pathLock is a lock on a given path. There are numPathLocks in this pool of locks and the
// string hash of a path selects which lock to use. This fixed pool ensures we don't have a growing
// number of locks and that we also don't have a race creating new locks when we first touch a path.

// All methods in storage.go are protected by a pathLock, which must be held upon calling them.

const numPathLocks = 100

var pathLocks [numPathLocks]sync.Mutex

// pathLock returns a mutex associated with a given path.
func pathLock(path upspin.PathName) *sync.Mutex {
	lockNum := hashCode(string(path))
	return &pathLocks[lockNum%numPathLocks]
}

func hashCode(s string) uint64 {
	h := uint64(0)
	for _, c := range s {
		h = 31*h + uint64(c)
	}
	return h
}
