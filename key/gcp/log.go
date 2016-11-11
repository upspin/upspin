// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
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

// logRef is the name in Google Cloud Storage under which the log is stored.
const logRef = "keyserver/log"

type logger struct {
	mu      sync.Mutex
	log     []byte
	storage storage.Storage
}

// PutAttempt records a KeyServer.Put attempt
// by the given actor for the given user record.
func (l *logger) PutAttempt(actor upspin.UserName, u *upspin.User) error {
	return l.put("put attempt", actor, u)
}

// PutSuccess records a successful KeyServer.Put
// by the given actor for the given user record.
func (l *logger) PutSuccess(actor upspin.UserName, u *upspin.User) error {
	return l.put("put success", actor, u)
}

var hashPrefix = []byte("SHA256:")

func (l *logger) put(kind string, actor upspin.UserName, u *upspin.User) error {
	content, err := json.Marshal(u)
	if err != nil {
		return err
	}

	now := time.Now().In(time.UTC)
	record := []byte(fmt.Sprintf("%v: %s by %q: %s\n", now, kind, actor, content))

	h := sha256.New()
	h.Write([]byte(record))

	l.mu.Lock()
	defer l.mu.Unlock()

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

	if err := l.populate(); err != nil {
		return err
	}

	l.log = append(l.log, record...)
	l.log = append(l.log, fmt.Sprintf("%s%x\n", hashPrefix, h.Sum(nil))...)

	_, err = l.storage.Put(logRef, l.log)
	return err
}

// ReadAll returns the log bytes.
func (l *logger) ReadAll() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.populate(); err != nil {
		return nil, err
	}

	// Make a copy of the log data to prevent tampering,
	// however unlikely that may be.
	data := append([]byte(nil), l.log...)
	return data, nil
}

// populate reads log data from storage and populates l.log.
// If l.log is already populated nothing happens.
// l.mu must be held.
func (l *logger) populate() error {
	if l.log != nil {
		return nil
	}
	href, err := l.storage.Get(logRef)
	if errors.Match(errors.E(errors.NotExist), err) {
		// The log doesn't exist yet.
		// Make l.log non-nil so that we don't keep trying.
		l.log = []byte{}
		return nil
	}
	if err != nil {
		return err
	}
	resp, err := http.Get(href)
	if err != nil {
		return err
	}
	data, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.Str(resp.Status)
	}
	l.log = data
	return nil
}
