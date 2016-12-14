// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package context

import "upspin.io/upspin"

// User returns an upspin.User record for the user in the given context.
func User(ctx upspin.Context) *upspin.User {
	var key upspin.PublicKey
	if f := ctx.Factotum(); f != nil {
		key = f.PublicKey()
	}
	return &upspin.User{
		Name:      ctx.UserName(),
		Dirs:      []upspin.Endpoint{ctx.DirEndpoint()},
		Stores:    []upspin.Endpoint{ctx.StoreEndpoint()},
		PublicKey: key,
	}
}
