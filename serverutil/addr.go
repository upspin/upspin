package serverutil

import (
	"net"

	"upspin.io/rpc/local"
)

// IsLoopback returns true if the name only resolves to loopback addresses.
func IsLoopback(addr string) bool {
	// Check for local IPC.
	if local.IsLocal(addr) {
		return true
	}

	// Check for loopback network.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}
