// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"sync"

	"upspin.io/upspin"
)

// A userLock is a lock on a given user. There are numUserLocks in this pool of locks and the
// string hash of a username selects which lock to use. This fixed pool ensures we don't have a growing
// number of locks and that we also don't have a race creating new locks when we first touch a user.

const numUserLocks = 100

var userLocks [numUserLocks]sync.Mutex

// userLock returns a mutex associated with a given user
func userLock(user upspin.UserName) *sync.Mutex {
	lockNum := hashCode(string(user))
	return &userLocks[lockNum%numUserLocks]
}

func hashCode(s string) uint64 {
	h := uint64(0)
	for _, c := range s {
		h = 31*h + uint64(c)
	}
	return h
}
