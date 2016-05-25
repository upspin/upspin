package auth

import (
	"time"

	"upspin.io/upspin"
)

// Config holds the configuration parameters for instantiating a server (HTTP or gRPC).
type Config struct {
	// Lookup looks up user keys.
	Lookup func(userName upspin.UserName) ([]upspin.PublicKey, error)

	// AllowUnauthenticatedConnections allows unauthenticated connections, making it the caller's
	// responsibility to check Handler.IsAuthenticated.
	AllowUnauthenticatedConnections bool

	// TimeFunc returns the current time. If nil, time.Now() will be used. Mostly only used for testing.
	TimeFunc func() time.Time
}
