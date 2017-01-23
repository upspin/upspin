#!/bin/sh

# Copyright 2017 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Build just the Upspin client tools, not the servers.
# Run from the directory containing this script.
set -e
go get -u upspin.io/cmd/upspin upspin.io/cmd/upspinfs upspin.io/cmd/cacheserver
# go install ./cmd/upspin ./cmd/upspinfs ./cmd/cacheserver
