// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package grpcauth

import (
	"google.golang.org/grpc"
)

// Start with GRPC tracing off.
func init() {
	grpc.EnableTracing = false
}
