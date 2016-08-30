// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build integration

package test

import (
	"fmt"
	"testing"

	"upspin.io/upspin"
)

func TestRemoteIntegration(t *testing.T) {
	kind := "remote"

	t.Run(fmt.Sprintf("kind=%v", kind), func(t *testing.T) {
		setup := setupTemplate
		setup.Kind = kind
		for _, p := range []struct {
			packing upspin.Packing
			curve   string
		}{
			{packing: upspin.PlainPack, curve: "p256"},
			{packing: upspin.DebugPack, curve: "p256"},
			{packing: upspin.EEPack, curve: "p256"},
			//{packing: upspin.EEPack, curve: "p521"}, // TODO: figure out if and how to test p521.
		} {
			setup.Packing = p.packing
			t.Run(fmt.Sprintf("packing=%v/curve=%v", p.packing, p.curve), func(t *testing.T) {
				testSelectedOnePacking(t, setup, allTests())
			})
		}
	})

}
