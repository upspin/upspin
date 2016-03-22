// Package auth handles authentication of Upspin users.
// This module implements common functionality between clients and server objects.
package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	// Header tagas must be in canonical format (first letter capitalized)
	userNameHeader      = "X-Upspin-Username"
	signatureHeader     = "X-Upspin-Signature"
	signatureTypeHeader = "X-Upspin-Signature-Type"
)

var (
	errMissingSignature = errors.New("missing signature in header")
)

func hashUserRequest(userName upspin.UserName, r *http.Request) []byte {
	sha := sha256.New()
	for k, v := range r.Header {
		if k == signatureHeader {
			// Do not hash the signature itself.
			continue
		}
		sha.Sum([]byte(fmt.Sprintf("%s:%s", k, v)))
	}
	// Request method (GET, PUT, etc)
	sha.Sum([]byte(r.Method))
	// The fully-formatted URL
	sha.Sum([]byte(r.URL.String()))
	// TODO: anything else?
	return sha.Sum(nil)
}
