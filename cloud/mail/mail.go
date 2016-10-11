// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mail defines an interface for sending mail that abstracts common
// functionality between all mail providers such as SendGrid, MailGun, etc.
package mail

// Mail sends email.
type Mail interface {
	// Send sends an email to a single recipient. The to address
	// must be fully specified such as name@domain.com. The from address
	// is just a name, the domain will be filled in as per the registered
	// domain (see Domain). Fields subject, textBody and htmlBody are as
	// expected. Subject is required and at least one of textBody or
	// htmlBody must be present.
	Send(to, from, subject, textBody, htmlBody string) error

	// Domain returns the domain name that is attached to the Mail object.
	// It is set during initialization and cannot be changed.
	Domain() string
}
