// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Userparser is a utility for parsing and validating upspin.User records.
package main

import (
	"bufio"
	"bytes"
	"strings"

	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/path"
	"upspin.io/upspin"
)

// States for a simple state machine for parsing the various fields of a user record.
const (
	none = iota
	name
	dir
	store
	key
)

// parseUser interprets buf as the output generated by the user command, validates each field and returns
// the User record.
func parseUser(buf []byte) (*upspin.User, error) {
	const parseUser = "parseUser"
	s := bufio.NewScanner(bytes.NewReader(buf))

	user := new(upspin.User)

	state := name
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		switch state {
		case name:
			if line == "" {
				// Ignore empty lines
				continue
			}
			err := validateUserName(line)
			if err != nil {
				return nil, err
			}
			user.Name = upspin.UserName(line)
			state = none
		case none:
			switch line {
			case "dirs {":
				state = dir
			case "stores {":
				state = store
			case "key {":
				state = key
			case "": // Ignore empty lines
			default:
				return nil, errors.E(parseUser, errors.Syntax, errors.Errorf("near %q", line))
			}
		case dir, store:
			switch line {
			case "}":
				state = none
			default:
				ep, err := upspin.ParseEndpoint(line)
				if err != nil {
					return nil, err
				}
				switch state {
				case dir:
					user.Dirs = append(user.Dirs, *ep)
				case store:
					user.Stores = append(user.Stores, *ep)
				default:
					return nil, errors.E(parseUser, errors.Syntax, errors.Str("internal inconsistency"))
				}
			}
		case key:
			pkey, err := readWholeKey(s, line)
			if err != nil {
				return nil, err
			}
			user.PublicKey = pkey
			state = none
		}
	}
	if s.Err() != nil {
		return nil, errors.E(parseUser, errors.IO, s.Err())
	}
	// Verify we have at least a valid user name.
	if user.Name != "" {
		return user, nil
	}
	return nil, errors.E(parseUser, errors.Syntax, errors.Str("invalid user record"))
}

func validateUserName(name string) error {
	_, _, err := path.UserAndDomain(upspin.UserName(name))
	return err
}

// readWholeKey reads the next lines from the scanner until the EOF or the end of a section delimited by "}" and
// verifies whether the set of the lines is a valid upspin public key.
func readWholeKey(scanner *bufio.Scanner, firstLine string) (upspin.PublicKey, error) {
	pkey := upspin.PublicKey(firstLine + "\n")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "}" {
			break
		}
		if line == "" {
			continue
		}
		pkey += upspin.PublicKey(line + "\n")
	}
	if scanner.Err() != nil {
		return "", errors.E(parseUser, errors.IO, scanner.Err())
	}
	// Validate key.
	_, _, err := factotum.ParsePublicKey(pkey)
	return pkey, err
}
