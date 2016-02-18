// Package unsafe implements a weak form of obfuscation. It is
// meant as a way to test a Packer integration with the rest of
// the system and should not be used in practice.
package unsafe

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"
)

// UnsafePack uses XOR for encrypting and signing.
type UnsafePack struct{}

var _ upspin.Packer = (*UnsafePack)(nil)

// aesKey is the symmetric key used for obfuscating the plain text data.
type aesKey []byte

// signature is the signature of a cleartext using the owner's private key.
type signature uint64

// wrappedKey is the encryption key used to encrypt the plain text
// encrypted with the named user's public key.
// TODO: fields are exported due to json encoding. Use binary instead
type wrappedKey struct {
	User    upspin.UserName
	Wrapped []byte
}

// clearMeta is what one gets after unpacking the metadata.
// TODO: fields are exported due to json encoding. Use binary instead
type clearMeta struct {
	WrappedKeys []wrappedKey
	Signature   signature
}

var (
	zeroKey        aesKey
	errKeyNotFound = errors.New("key not found")
)

func init() {
	pack.Register(&UnsafePack{})
}

// Packing implements the Packer interface
func (u *UnsafePack) Packing() upspin.Packing {
	return upspin.UnsafePack
}

// unpackMeta traverses the packdata portion of the metadata and
// unpacks it, returning the wrapped keys as well as a signature.
func (u *UnsafePack) unpackMeta(meta []byte) *clearMeta {
	if meta == nil || len(meta) == 0 {
		return nil
	}
	var clear clearMeta
	err := json.Unmarshal(meta, &clear)
	if err != nil {
		return nil
	}
	return &clear
}

func (u *UnsafePack) packMeta(meta *clearMeta) []byte {
	enc, err := json.Marshal(*meta)
	if err != nil {
		return nil
	}
	return enc
}

// Pack implements the Packer interface
func (u *UnsafePack) Pack(context *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if meta == nil {
		return 0, errors.New("nil metadata")
	}
	clear := u.unpackMeta(meta.PackData)
	if clear == nil {
		clear = &clearMeta{}
	}

	// Get private key for the current user.
	if len(context.PrivateKey) == 0 {
		return 0, errors.New("empty private key for current user")
	}

	// Get an AES key, either from the metadata if present or by
	// creating a new one from scratch.
	var aesKey aesKey
	wrapped, err := u.extractWrappedKeyFromMeta(context, clear)
	if err != nil {
		log.Println("No wrapped key in meta. Creating new AES key")
		aesKey, err = u.genUnsoundKey(context.UserName, name)
		if err != nil {
			return 0, err
		}
	} else {
		aesKey = xor(wrapped.Wrapped, context.PrivateKey)
	}

	// Encrypt the cleartext with the AES key.
	buf := xor(cleartext, aesKey)
	copy(ciphertext, buf)

	// Sign it with the user's private key.
	clear.Signature = sign(cleartext, context.PrivateKey)

	// Re-generate the metadata. All cached users get their own wrapped keys.
	clear.WrappedKeys = nil
	for _, user := range context.User.(*testuser.Service).ListUsers() {
		// Find keys for a user
		_, keys, err := context.User.Lookup(user)
		if err != nil || len(keys) == 0 {
			log.Printf("Skipping sharing with user %v", user)
			continue
		}
		// Prefer the first key returned
		log.Printf("Wrapping keys for user: %v", user)
		wk := wrappedKey{
			User:    user,
			Wrapped: xor(aesKey, keys[0]),
		}
		clear.WrappedKeys = append(clear.WrappedKeys, wk)
	}
	meta.PackData = u.packMeta(clear)

	return len(buf), nil
}

// Unpack implements the Packer interface
func (u *UnsafePack) Unpack(context *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if meta == nil {
		return 0, errors.New("nil metadata")
	}
	clear := u.unpackMeta(meta.PackData)
	if clear == nil {
		return 0, errors.New("missing metadata")
	}

	// Find our key in the wrapped keys
	wrapped, err := u.extractWrappedKeyFromMeta(context, clear)
	if err != nil {
		return 0, err
	}

	// Get private key of the current user
	privateKey := context.PrivateKey
	if len(privateKey) == 0 {
		return 0, fmt.Errorf("no private key for user %v", context.UserName)
	}

	// Decrypt our wrapped key.
	log.Printf("Keys found for user %v", context.UserName)
	aesKey := xor(wrapped.Wrapped, privateKey)

	// Decrypt the ciphertext
	buf := xor(ciphertext, aesKey)
	copy(cleartext, buf)
	cleartext = cleartext[:len(buf)]

	// Validate signature
	// First, find the owner's public key.
	p, err := path.Parse(name)
	if err != nil {
		return len(buf), errors.New("can't parse path")
	}
	fileOwner := p.User
	log.Printf("File owner found: %s", fileOwner)

	_, keys, err := context.User.Lookup(fileOwner)
	if err != nil || len(keys) == 0 {
		return 0, fmt.Errorf("no keys for owner user of %v: %v", name, fileOwner)
	}

	sig := sign(cleartext, keys[0])
	if sig != clear.Signature {
		err := fmt.Sprintf("expected signature %v, got %v", clear.Signature, sig)
		log.Println(err)
		return len(buf), errors.New(err)
	}

	return len(buf), nil
}

// PackLen implements the Packer interface
func (u *UnsafePack) PackLen(context *upspin.Context, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

// UnpackLen implements the Packer interface
func (u *UnsafePack) UnpackLen(context *upspin.Context, ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

// extractWrappedKeyFromMeta returns the AES key stored in the user's own
// wrappedKeys or an error if no such information is found in the
// metadata.
func (u *UnsafePack) extractWrappedKeyFromMeta(context *upspin.Context, clearMeta *clearMeta) (wrappedKey, error) {
	var wrapped wrappedKey
	for _, wk := range clearMeta.WrappedKeys {
		if string(wk.User) == string(context.UserName) {
			log.Printf("Wrapped Keys found for user: %v", context.UserName)
			wrapped = wk
		}
	}
	if len(wrapped.User) == 0 {
		return wrapped, errKeyNotFound
	}

	return wrapped, nil
}

// genUnsoundKey generates a random, unique key for a given user and
// path name that is not cryptographically sound.
func (u *UnsafePack) genUnsoundKey(userName upspin.UserName, pathName upspin.PathName) (aesKey, error) {
	buf := make([]byte, 8)
	n, err := io.ReadFull(rand.Reader, buf)
	if n < cap(buf) || err != nil {
		return zeroKey, errors.New("error during key generation")
	}
	return buf, nil
}

func xor(contents []byte, key []byte) []byte {
	buf := make([]byte, len(contents))

	for i, b := range contents {
		buf[i] = b ^ key[i%len(key)]
	}
	return buf
}

func sign(contents []byte, key []byte) signature {
	if len(key) == 0 {
		return 0
	}
	sig := uint64(0)
	for i, v := range contents {
		sig = sig + uint64(v)*uint64(key[i%len(key)])
	}
	return signature(sig)
}
