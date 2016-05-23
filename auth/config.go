package auth

import (
	"time"

	"upspin.googlesource.com/upspin.git/upspin"
)

// Config holds the configuration parameters for an instance of Handler.
type Config struct {
	// Lookup looks up user keys.
	Lookup func(userName upspin.UserName) ([]upspin.PublicKey, error)

	// AllowUnauthenticatedConnections allows unauthenticated connections, making it the caller's responsibility to check Handler.IsAuthenticated.
	AllowUnauthenticatedConnections bool

	// TimeFunc returns the current time. If nil, time.Now() will be used. Mostly only used for testing.
	TimeFunc func() time.Time
}
