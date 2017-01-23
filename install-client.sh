#!/bin/sh

# Copyright 2017 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# Build just the Upspin client tools, not the servers.
set -e
cd $GOPATH/src/upspin.io
go install ./cmd/upspin ./cmd/upspinfs ./cmd/cacheserver
