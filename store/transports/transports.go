// Package transports is a helper package that aggregates the store imports.
// It has no functionality itself; it is meant to be imported, using an "underscore"
// import, as a convenient way to link with all the transport implementations.
package transports

import (
	_ "upspin.io/store/gcp"
	_ "upspin.io/store/https"
	_ "upspin.io/store/inprocess"
	_ "upspin.io/store/remote"
)
