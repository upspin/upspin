// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"

	"upspin.io/upspin"
)

// This file contains simple routines to make displaying of the user record
// in JSON look nicer and more human-readable.

// userDisplay is a mirror version of upspin.User with some fields replaced with
// strings for better human reading. It must be kept in-sync with upspin.User.
// The fields are exported so JSON marshal and unmarshal can access them. They are
// not meant to be used other than for displaying purposes.
type userDisplay struct {
	Name      upspin.UserName  `json:"name"`
	Dirs      []string         `json:"dirs"`
	Stores    []string         `json:"stores"`
	PublicKey upspin.PublicKey `json:"publicKey"`
}

func userMarshalJSON(user *upspin.User) ([]byte, error) {
	dirs := toStringSlice(user.Dirs)
	stores := toStringSlice(user.Stores)
	u := &userDisplay{
		Name:      user.Name,
		Dirs:      dirs,
		Stores:    stores,
		PublicKey: user.PublicKey,
	}
	buf, err := json.MarshalIndent(u, "", "\t")
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func userUnmarshalJSON(buf []byte) (*upspin.User, error) {
	var u userDisplay
	err := json.Unmarshal(buf, &u)
	if err != nil {
		return nil, err
	}
	dirs, err := toEndpoints(u.Dirs)
	if err != nil {
		return nil, err
	}
	stores, err := toEndpoints(u.Stores)
	if err != nil {
		return nil, err
	}
	user := &upspin.User{
		Name:      u.Name,
		Dirs:      dirs,
		Stores:    stores,
		PublicKey: u.PublicKey,
	}
	return user, nil
}

func toStringSlice(endPts []upspin.Endpoint) []string {
	var str []string
	for _, ep := range endPts {
		str = append(str, ep.String())
	}
	return str
}

func toEndpoints(strEndPts []string) ([]upspin.Endpoint, error) {
	var endPts []upspin.Endpoint
	for _, str := range strEndPts {
		ep, err := upspin.ParseEndpoint(str)
		if err != nil {
			return nil, err
		}
		endPts = append(endPts, *ep)
	}
	return endPts, nil
}
