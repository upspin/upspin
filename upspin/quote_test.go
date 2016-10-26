// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspin

import "testing"

type QuoteGlobTest struct {
	in  PathName
	out PathName
}

var quoteGlobTests = []QuoteGlobTest{
	{``, ``},
	{`a`, `a`},
	{`\`, `\\`},
	{`\\`, `\\\\`},
	{`[`, `\[`},
	{`a\*`, `a\\\*`},
}

func TestQuoteGlob(t *testing.T) {
	for _, test := range quoteGlobTests {
		got := QuoteGlob(test.in)
		if got != test.out {
			t.Errorf("QuoteGlob(%#q) = %#q; want %#q", test.in, got, test.out)
		}
	}
}
