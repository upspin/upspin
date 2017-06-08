// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Command upbox builds and runs Upspin servers as specified by a schema and
provides an upspin shell acting as the first user specified by the schema.

For information on defining a schema, see the documentation for package
upspin.io/exp/schema.
*/
package main

import (
	"flag"
	"fmt"
	"os"

	"upspin.io/exp/schema"
)

var (
	logLevel   = flag.String("log", "info", "log `level`")
	basePort   = flag.Int("port", 8000, "base `port` number for upspin servers")
	schemaFile = flag.String("schema", "", "schema `file` name")
)

func main() {
	flag.Parse()

	sc, err := schema.FromFile(*schemaFile, *basePort)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upbox: error parsing schema:", err)
		os.Exit(1)
	}

	if err := sc.Run(*logLevel); err != nil {
		fmt.Fprintln(os.Stderr, "upbox:", err)
		os.Exit(1)
	}
}
