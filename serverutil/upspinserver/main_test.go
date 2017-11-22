// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package upspinserver

import (
	"testing"
)

func TestCredentialsHiding(t *testing.T) {
	testCases := []struct {
		input  []string
		output string
	}{
		{[]string{}, ""},
		{[]string{"token=apiToken"}, ""},
		{[]string{"gcpBucketName=bucket", "defaultACL=acl", "privateKeyData=key"}, "gcpBucketName=bucket defaultACL=acl"},
		{[]string{"b2csAccount=account", "b2csAppKey=key", "b2csBucketName=bucket"}, "b2csAccount=account b2csBucketName=bucket"},
		{[]string{"openstackContainer=upspin", "openstackRegion=region", "openstackAuthURL=url", "privateOpenstackTenantName=tenant",
			"privateOpenstackUsername=user", "privateOpenstackPassword=password", "privateOpenstackPassword=password"},
			"openstackContainer=upspin openstackRegion=region openstackAuthURL=url"},
	}
	for i, c := range testCases {
		output := fmtStoreConfig(c.input)
		if c.output != output {
			t.Errorf("case %d: got %v, want %v", i, output, c.output)
		}
	}
}
