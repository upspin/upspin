// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server // import "upspin.io/key/server"

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/upspin"
)

// Logger returns KeyServer logs for auditing purposes.
// It is implemented by the KeyServer in this package,
// but is not part of the upspin.KeyServer interface.
type Logger interface {
	Log() ([]byte, error)
}

type logger interface {
	PutAttempt(actor upspin.UserName, u *upspin.User) error
	PutSuccess(actor upspin.UserName, u *upspin.User) error
	ReadAll() ([]byte, error)
}

// logRef is the name in Google Cloud Storage under which the log is stored.
const logRef = "keyserver/log"

type loggerImpl struct {
	mu      sync.Mutex
	log     []byte
	storage storage.Storage
}

// PutAttempt records a KeyServer.Put attempt
// by the given actor for the given user record.
func (l *loggerImpl) PutAttempt(actor upspin.UserName, u *upspin.User) error {
	const op errors.Op = errors.Op("key/gcp.Logger.PutAttempt")
	if err := l.put(time.Now(), "put attempt", actor, u); err != nil {
		return errors.E(op, err)
	}
	return nil
}

// PutSuccess records a successful KeyServer.Put
// by the given actor for the given user record.
func (l *loggerImpl) PutSuccess(actor upspin.UserName, u *upspin.User) error {
	const op errors.Op = errors.Op("key/gcp.Logger.PutSuccess")
	if err := l.put(time.Now(), "put success", actor, u); err != nil {
		return errors.E(op, err)
	}
	return nil
}

var hashPrefix = []byte("SHA256:")

func (l *loggerImpl) put(now time.Time, kind string, actor upspin.UserName, u *upspin.User) error {
	content, err := json.Marshal(u)
	if err != nil {
		return err
	}

	record := []byte(fmt.Sprintf("%v: %s by %q: %s\n", now.In(time.UTC), kind, actor, content))

	h := sha256.New()
	h.Write([]byte(record))

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.populate(); err != nil {
		return err
	}

	// Include the previous record's hash in the new hash,
	// but only if there is a previous record.
	i := bytes.LastIndex(l.log, hashPrefix)
	if i == -1 {
		if len(l.log) > 0 {
			return errors.Str("key log corrupt: non-empty but lacks previous record hash")
		}
		// No previous record; that's ok.
	} else {
		// Grab the hash hex, stripping the prefix and trailing newline.
		prevHash := l.log[i+len(hashPrefix) : len(l.log)-1]
		h.Write(prevHash)
	}

	l.log = append(l.log, record...)
	l.log = append(l.log, fmt.Sprintf("%s%x\n", hashPrefix, h.Sum(nil))...)

	return l.storage.Put(logRef, l.log)
}

// ReadAll returns the log bytes.
func (l *loggerImpl) ReadAll() ([]byte, error) {
	const op errors.Op = "key/gcp.Logger.ReadAll"
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.populate(); err != nil {
		return nil, errors.E(op, err)
	}

	// Make a copy of the log data to prevent tampering,
	// however unlikely that may be.
	data := append([]byte(nil), l.log...)
	return data, nil
}

// populate reads log data from storage and populates l.log.
// If l.log is already populated nothing happens.
// l.mu must be held.
func (l *loggerImpl) populate() error {
	if l.log != nil {
		return nil
	}
	data, err := l.storage.Download(logRef)
	if errors.Is(errors.NotExist, err) {
		// The log doesn't exist yet.
		// Make l.log non-nil so that we don't keep trying.
		l.log = []byte{}
		return nil
	}
	if err != nil {
		return err
	}
	l.log = data
	return nil
}
