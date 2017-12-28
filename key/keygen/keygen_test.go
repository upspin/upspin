// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package keygen

import (
	"strings"
	"testing"
)

var keygenTestCases = []struct {
	secret   string
	proquint string
}{
	{"secretstringforu", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj"},
	{"asdaassdwerdsgfd", "kajug-kidod-kajug-latoh.litoj-lanoh-latol-kinoh"},
	{"abracadabramatey", "kajof-lanod-katod-kidod.kanuf-kajot-kajuh-kijun"},
	{"!!!!!!!!!!!!!!!!", "fahod-fahod-fahod-fahod.fahod-fahod-fahod-fahod"},
}

func TestTypeSecretProquintMethod(t *testing.T) {
	for _, c := range keygenTestCases {
		var sec secret
		copy(sec[:], c.secret)
		if sec.proquint() != c.proquint {
			t.Errorf("%+v.proquint() should be %q, got %q", sec, c.proquint, sec.proquint())
		}
	}
}

func TestSecretFromProquint(t *testing.T) {
	for _, c := range keygenTestCases {
		var sec secret
		copy(sec[:], c.secret)
		if secretFromProquint(c.proquint) != sec {
			t.Errorf("secretFromProquint(%q) should be %q, got %q", c.proquint, sec, secretFromProquint(c.proquint))
		}
	}
}

func TestFromSecret(t *testing.T) {
	cases := []struct {
		curve  string
		seed   string
		pubkey string
		valid  bool
	}{
		{"p256", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "p256\n605083556", true},
		{"p384", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "p384\n185051353", true},
		{"p521", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "p521\n608669811", true},
		{"p123", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "nope", false},
	}

	for _, c := range cases {
		pubkey, _, secret, err := FromSecret(c.curve, c.seed)
		if err != nil && c.valid {
			t.Error(err)
			continue
		}
		if err == nil && !c.valid {
			t.Errorf("FromSecret(%q, %q) should raise an error but didn't", c.curve, c.seed)
			continue
		}
		if c.valid && !strings.Contains(pubkey, c.pubkey) {
			if len(pubkey) > 16 {
				pubkey = pubkey[:16]
			}
			t.Errorf("FromSecret(%q, %q) should give %q... as public key, gave %q...", c.curve, c.seed, c.pubkey, pubkey)
			continue
		}
		if c.valid && secret != c.seed {
			t.Errorf("FromSecret(%q, %q) should give %q as secret, gave %q", c.curve, c.seed, c.seed, secret)
		}
	}
}

func TestValidSecretSeed(t *testing.T) {
	cases := []struct {
		proquint string
		valid    bool
	}{
		{"babab-babab-babab-babab.babab-babab-babab-babab", true},
		{"disis-valid-fosoh-matij.disis-valid-fosoh-matij", true},
		{"babab", false},
		{"bbbaa-bbbaa-bbbaa-bbbaa.bbbaa-bbbaa-bbbaa-bbbaa", false},
		{"babab-babab-babab-babab-babab-babab-babab-babab", false},
		{"disis-valid-fosho-matey.disis-valid-fosho-matey", false},
		{"babab-babab-babab-babab.babab-babab-babab-babab/", false},
		{"/babab-babab-babab-babab.babab-babab-babab-babab", false},
		{"", false},
		{"83838-83838-83838-83838.83838-83838-83838-83838", false},
	}

	for _, c := range cases {
		if ValidSecretSeed(c.proquint) != c.valid {
			t.Errorf("ValidSecretSeed(%q) returned %t, should be %t", c.proquint, !c.valid, c.valid)
		}
	}
}
