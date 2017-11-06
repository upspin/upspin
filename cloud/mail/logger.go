// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mail

import "upspin.io/log"

// Logger returns a Mail implementation that logs any sent messages to the
// given Logger.
func Logger(l log.Logger) Mail {
	return &logger{l}
}

type logger struct {
	logger log.Logger
}

func (l logger) Send(to, from, subject, text, html string) error {
	l.logger.Printf("mail to=%q from=%q subject=%q:\n%s", to, from, subject, text)
	return nil
}
