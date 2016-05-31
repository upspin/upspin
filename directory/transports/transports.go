// Package transports is a helper package that aggregates the directory imports.
// It has no functionality itself; it is meant to be imported, using an "underscore"
// import, as a convenient way to link with all the transport implementations.
package transports

import (
	_ "upspin.io/directory/gcpdir"
	_ "upspin.io/directory/inprocess"
	_ "upspin.io/directory/remote"
)
