// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"sync"

	"upspin.io/upspin"
)

const (
	numPathLocks = 100
	numUserLocks = 10
)

var (
	pathLocks []*sync.Mutex
	userLocks []*sync.Mutex
)

// pathLock returns a mutex associated with a given path.
func pathLock(path upspin.PathName) *sync.Mutex {
	lockNum := hashCode(string(path))
	return pathLocks[lockNum%numPathLocks]
}

// userLock returns a mutex associated with a given username.
func userLock(userName upspin.UserName) *sync.Mutex {
	lockNum := hashCode(string(userName))
	return userLocks[lockNum%numUserLocks]
}

func hashCode(s string) uint64 {
	h := uint64(0)
	for _, c := range s {
		h = 31*h + uint64(c)
	}
	return h
}

func init() {
	pathLocks = make([]*sync.Mutex, numPathLocks)
	for i := range pathLocks {
		pathLocks[i] = new(sync.Mutex)
	}
	userLocks = make([]*sync.Mutex, numUserLocks)
	for i := range userLocks {
		userLocks[i] = new(sync.Mutex)
	}
}
