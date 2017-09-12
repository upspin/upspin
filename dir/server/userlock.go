// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"sync"

	"upspin.io/upspin"
)

const numUserLocks = 100

// userLock locks and returns the mutex associated with the user.
func (s *server) userLock(user upspin.UserName) *sync.Mutex {
	mu := &s.userLocks[hashCode(string(user))%numUserLocks]
	mu.Lock()
	return mu
}

func hashCode(s string) uint64 {
	h := uint64(123479)
	for _, c := range s {
		h = 31*h + uint64(c)
	}
	return h
}
