// Package transports is a helper package that aggregates the user imports.
// It has no functionality itself; it is meant to be imported, using an "underscore"
// import, as a convenient way to link with all the transport implementations.
package transports

import (
	_ "upspin.io/user/gcpuser"
	_ "upspin.io/user/inprocess"
	_ "upspin.io/user/remote"
)
