// Package auth handles authentication of Upspin users.
// This module implements common functionality between clients and server objects.
package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	userNameHeader  = "X-Upspin-UserName"
	signatureHeader = "X-Upspin-Signature"
	signatureType   = "X-Upspin-Signature-Type"
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

// SignRequest sets the necessary headers in the HTTP request to authenticate a user, by signing the request with the given key.
func SignRequest(userName upspin.UserName, keys upspin.KeyPair, req *http.Request) error {
	req.Header.Set(userNameHeader, string(userName)) // Set the username
	ecdsaPubKey, keyType, err := parsePublicKey(keys.Public)
	if err != nil {
		return err
	}
	req.Header.Set(signatureType, keyType)
	ecdsaPrivKey, err := parsePrivateKey(ecdsaPubKey, keys.Private)
	if err != nil {
		return err
	}
	// The hash includes the user name and the key type.
	hash := hashUserRequest(userName, req)
	r, s, err := ecdsa.Sign(rand.Reader, ecdsaPrivKey, hash)
	if err != nil {
		return err
	}
	req.Header.Set(signatureHeader, fmt.Sprintf("%s %s", r, s))
	return nil
}

// verifyRequest verifies whether named user has signed the HTTP request using one of the possible keys.
func verifyRequest(userName upspin.UserName, keys []upspin.PublicKey, req *http.Request) error {
	sig := req.Header.Get(signatureHeader)
	if sig == "" {
		return errors.New("no signature in header")
	}
	neededKeyType := req.Header.Get(signatureType)
	if neededKeyType == "" {
		return errors.New("no signature type in header")
	}
	sigPieces := strings.Fields(sig)
	if len(sigPieces) != 2 {
		return fmt.Errorf("expected two integers in signature, got %d", len(sigPieces))
	}
	var rs, ss big.Int
	_, ok := rs.SetString(sigPieces[0], 10)
	if !ok {
		return errMissingSignature
	}
	_, ok = ss.SetString(sigPieces[1], 10)
	if !ok {
		return errMissingSignature
	}
	for _, k := range keys {
		ecdsaPubKey, keyType, err := parsePublicKey(k)
		if err != nil {
			return err
		}
		if keyType != neededKeyType {
			continue
		}
		hash := hashUserRequest(userName, req)
		if !ecdsa.Verify(ecdsaPubKey, hash, &rs, &ss) {
			return fmt.Errorf("signature verification failed for user %s", userName)
		}
		return nil
	}
	return fmt.Errorf("no keys found for user %s", userName)
}

// TODO: the following were copied and slightly adapted from pack/ee.go. Move to a common place.
// parsePrivateKey returns an ECDSA private key given a user's ECDSA public key and a
// string representation of the private key.
func parsePrivateKey(publicKey *ecdsa.PublicKey, privateKey upspin.PrivateKey) (priv *ecdsa.PrivateKey, err error) {
	n := len(privateKey) - 1
	if n > 0 && privateKey[n] == '\n' {
		privateKey = privateKey[:n]
	}
	var d big.Int
	err = d.UnmarshalText([]byte(privateKey))
	if err != nil {
		return nil, err
	}
	return &ecdsa.PrivateKey{PublicKey: *publicKey, D: &d}, nil
}

// parsePublicKey takes a string representation of a public key and converts it into an ECDSA public key, returning its type.
func parsePublicKey(publicKey upspin.PublicKey) (*ecdsa.PublicKey, string, error) {
	var keyType string
	var x, y big.Int

	n, err := fmt.Fscan(bytes.NewReader([]byte(publicKey)), &keyType, &x, &y)
	if err != nil {
		return nil, "", err
	}
	if n != 3 {
		return nil, "", fmt.Errorf("expected keytype and two big ints, got %d", n)
	}
	var curve elliptic.Curve
	switch keyType {
	case "p256":
		curve = elliptic.P256()
	case "p521":
		curve = elliptic.P521()
	case "p384":
		curve = elliptic.P384()
	default:
		return nil, "", errors.New("unknown key type")
	}
	return &ecdsa.PublicKey{Curve: curve, X: &x, Y: &y}, keyType, nil
}
