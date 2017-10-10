// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storagetest

import (
	"sync"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// Memory returns a storage.Storage implementation that stores data in memory.
// It is safe for concurrent use.
func Memory() storage.Storage {
	return &mem{
		m: make(map[string][]byte),
	}
}

type mem struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func (m *mem) LinkBase() (base string, err error) {
	return "", upspin.ErrNotSupported
}

func (m *mem) Download(ref string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.m[ref]
	if !ok {
		return nil, errors.E(errors.NotExist, errors.Str(ref))
	}
	return append([]byte{}, b...), nil
}

func (m *mem) Put(ref string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.m[ref] = append([]byte{}, b...)
	return nil
}

func (m *mem) Delete(ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.m[ref]
	if !ok {
		return errors.E(errors.NotExist, errors.Str(ref))
	}
	delete(m.m, ref)
	return nil
}
