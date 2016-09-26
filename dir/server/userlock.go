// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"sync"

	"upspin.io/upspin"
)

const numUserLocks = 100

// userLock returns a mutex associated with a given user.
func (s *server) userLock(user upspin.UserName) *sync.Mutex {
	lockNum := hashCode(string(user))
	return &s.userLocks[lockNum%numUserLocks]
}

func hashCode(s string) uint64 {
	h := uint64(0)
	for _, c := range s {
		h = 31*h + uint64(c)
	}
	return h
}
