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

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/upspin"
)

const logRef = "keyserver/log"

type logger struct {
	mu      sync.Mutex
	log     []byte
	storage storage.Storage
}

func (l *logger) PutAttempt(actor upspin.UserName, u *upspin.User) error {
	return l.put("put attempt", actor, u)
}

func (l *logger) PutSuccess(actor upspin.UserName, u *upspin.User) error {
	return l.put("put success", actor, u)
}

var hashPrefix = []byte("SHA256:")

func (l *logger) put(kind string, actor upspin.UserName, u *upspin.User) error {
	content, err := json.Marshal(u)
	if err != nil {
		return err
	}

	record := []byte(fmt.Sprintf("%s by %q: %s\n", kind, actor, content))

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

	l.log = append(l.log, record...)
	l.log = append(l.log, fmt.Sprintf("%s%x\n", hashPrefix, h.Sum(nil))...)

	_, err = l.storage.Put(logRef, l.log)
	return err
}

func (l *logger) Read(offset int64) ([]byte, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.log == nil {
		href, err := l.storage.Get(logRef)
		if err != nil {
			return nil, 0, err
		}
		resp, err := http.Get(href)
		if err != nil {
			return nil, 0, err
		}
		data, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			l.log = data
		case http.StatusNotFound:
			// The log doesn't exist yet;
			// make log non-nil so that we
			// don't keep trying.
			l.log = []byte{}
		default:
			return nil, 0, errors.Str(resp.Status)
		}
	}

	if int(offset) > len(l.log) || int(offset) < 0 {
		// Offset outside file range.
		return nil, 0, errors.Errorf("invalid offset: %v", offset)
	}
	if int(offset) == len(l.log) {
		// Nothing has changed.
		return nil, offset, nil
	}

	// Make a copy of the log data to prevent tampering,
	// however unlikely that may be.
	data := append([]byte(nil), l.log[int(offset):]...)
	return data, int64(len(l.log)), nil
}
