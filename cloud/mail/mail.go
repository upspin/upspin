// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mail defines an interface for sending mail that abstracts common
// functionality between all mail providers such as SendGrid or MailGun.
package mail // import "upspin.io/cloud/mail"

// Mail sends email.
type Mail interface {
	// Send sends an email to a single recipient. The to address
	// must be fully specified such as name@domain.com. The from address
	// is just a name, the domain will be filled in as per the registered
	// domain (see Domain). Fields subject, text and html are as
	// expected. Subject is required and at least one of text or
	// html must be present.
	Send(to, from, subject, text, html string) error
}
