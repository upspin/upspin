// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package keygen

import "testing"

func keygenTestCases() map[string]string {
	return map[string]string{
		"secretstringforu": "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj",
		"asdaassdwerdsgfd": "kajug-kidod-kajug-latoh.litoj-lanoh-latol-kinoh",
		"abracadabramatey": "kajof-lanod-katod-kidod.kanuf-kajot-kajuh-kijun",
		"!!!!!!!!!!!!!!!!": "fahod-fahod-fahod-fahod.fahod-fahod-fahod-fahod",
	}
}

func TestTypeSecretProquintMethod(t *testing.T) {
	for k, v := range keygenTestCases() {
		var s secret
		copy(s[:], k)
		if s.proquint() != v {
			t.Errorf("%+v.proquint() should be %q, got %q", s, v, s.proquint())
		}
	}
}

func TestSecretFromProquint(t *testing.T) {
	for k, v := range keygenTestCases() {
		var s secret
		copy(s[:], k)
		if secretFromProquint(v) != s {
			t.Errorf("secretFromProquint(%q) should be %q, got %q", v, s, secretFromProquint(v))
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
		{"p256", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "p256\n60508355662318538249198873524300416221019578062710182543907821501645742773811\n104215045541332960307034755944355337661524160338432951899713581551742281333144\n", true},
		{"p384", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "p384\n18505135379870859714764757188858181034030723775650769448716864641375278571607358669694505224309250805421981287323975\n4398927826815297415271446069360334900247381781465905924053404658702922003613050323478254753321539228872445631760504\n", true},
		{"p521", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "p521\n6086698110678057573406676605409146473537689817116578686518368059322945357383070931662913281261986233712880819959478455856101205234505994610828360389749851285\n4661027142556325591886797563550502077634248203404809691549542538279866275356099354996513396469354223107557006863629710762995762954653442939165621603242169941\n", true},
		{"p123", "latoj-katuf-kijuh-latuh.lanon-kunol-kinoz-lanuj", "nope", false},
	}

	for _, c := range cases {
		pubkey, _, secret, err := FromSecret(c.curve, c.seed)
		if err != nil && c.valid {
			t.Error(err)
		}
		if err == nil && !c.valid {
			t.Errorf("FromSecret(%q, %q) should raise an error but didn't", c.curve, c.seed)
		}
		if c.valid && pubkey != c.pubkey {
			t.Errorf("FromSecret(%q, %q) should give %q as public key, gave %q", c.curve, c.seed, c.pubkey, pubkey)
		}
		if c.valid && secret != c.seed {
			t.Errorf("FromSecret(%q, %q) should give %q as secret, gave %q", c.curve, c.seed, c.seed, secret)
		}
	}
}

func TestValidSecretSeed(t *testing.T) {
	cases := map[string]bool{
		"babab-babab-babab-babab.babab-babab-babab-babab": true,
		"disis-valid-fosoh-matij.disis-valid-fosoh-matij": true,
		"noodle":                                           false,
		"beers-beers-beers-beers":                          false,
		"beers-beers-beers-beers.beers-beers-beers-beers":  false,
		"disis-valid-fosho-matey.disis-valid-fosho-matey":  false,
		"babab-babab-babab-babab.babab-babab-babab-babab/": false,
		"/babab-babab-babab-babab.babab-babab-babab-babab": false,
		"": false,
		"83838-83838-83838-83838.83838-83838-83838-83838": false,
	}

	for k, v := range cases {
		if ValidSecretSeed(k) != v {
			t.Errorf("ValidSecretSeed(%q) returned %t, should be %t", k, !v, v)
		}
	}
}
