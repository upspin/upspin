// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import "upspin.io/upspin"

type noopLogger struct{}

func (noopLogger) PutAttempt(actor upspin.UserName, u *upspin.User) error {
	return nil
}
func (noopLogger) PutSuccess(actor upspin.UserName, u *upspin.User) error {
	return nil
}
func (noopLogger) ReadAll() ([]byte, error) {
	return nil, nil
}
