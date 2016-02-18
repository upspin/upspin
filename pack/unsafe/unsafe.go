// Package unsafe implements a weak form of obfuscation. It is
// meant as a way for testing a Packer integration with the rest of
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
)

// UnsafePack is exported because we need to access methods that are
// specific to end-to-end packing, such as setting public and private
// keys.
type UnsafePack struct {
	// keystore stores known keys for users.
	keystore map[upspin.UserName]userKeys

	// user is the current user
	user upspin.UserName
}

var _ upspin.Packer = (*UnsafePack)(nil)

// simKey is the symmetric key used for obfuscating the plain text data.
type aesKey []byte

// signature is the signature of a cleartext using the owners private key.
type signature uint64

// userKeys stores the public and private key of each user.
type userKeys struct {
	user    upspin.UserName
	public  []byte
	private []byte
}

// wrappedKey is the encryption key used to encrypt the plain text
// encrypted with the named user's public key.
type wrappedKey struct {
	User    upspin.UserName
	Wrapped []byte
}

// clearMeta is what one gets after unpacking the metadata.
type clearMeta struct {
	WrappedKeys []wrappedKey
	Signature   signature
}

var (
	zeroKey        aesKey
	errKeyNotFound = errors.New("key not found")
)

func init() {
	pack.Register(&UnsafePack{
		keystore: make(map[upspin.UserName]userKeys, 10),
	})
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
func (u *UnsafePack) Pack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if meta == nil {
		return 0, errors.New("nil metadata")
	}
	clear := u.unpackMeta(meta.PackData)
	if clear == nil {
		clear = &clearMeta{}
	}

	// Get a pair of keys for the current user
	keyPair, found := u.keystore[u.user]
	if !found {
		return 0, errors.New("no keys for current user")
	}
	if len(keyPair.private) == 0 {
		return 0, errors.New("empty private key for current user")
	}

	// Get an AES key, either from the metadata if present or by
	// creating a new one from scratch.
	var aesKey aesKey
	wrapped, err := u.extractWrappedKeyFromMeta(clear)
	if err != nil {
		log.Println("No wrapped key in meta. Creating new AES key")
		aesKey, err = u.genUnsoundKey(u.user, name)
		if err != nil {
			return 0, err
		}
	} else {
		aesKey = u.xor(wrapped.Wrapped, keyPair.private)
	}
	log.Printf("AES Key: %s", aesKey)

	// Encrypt the cleartext with the AES key.
	buf := u.xor(cleartext, aesKey)
	copy(ciphertext, buf)

	// Sign it with the user's private key.
	clear.Signature = sign(cleartext, keyPair.private)

	// Re-generate the metadata. All cached users get their own wrapped keys.
	clear.WrappedKeys = nil
	for user, keys := range u.keystore {
		log.Printf("Wrapping keys for user: %v, pubkey: %v", user, keys.public)
		wk := wrappedKey{
			User:    user,
			Wrapped: u.xor(aesKey, keys.public),
		}
		clear.WrappedKeys = append(clear.WrappedKeys, wk)
	}
	meta.PackData = u.packMeta(clear)

	return len(buf), nil
}

// Unpack implements the Packer interface
func (u *UnsafePack) Unpack(cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if meta == nil {
		return 0, errors.New("nil metadata")
	}
	clear := u.unpackMeta(meta.PackData)
	if clear == nil {
		return 0, errors.New("missing metadata")
	}

	// Find our key in the wrapped keys
	wrapped, err := u.extractWrappedKeyFromMeta(clear)
	if err != nil {
		return 0, err
	}

	// Get private key of current user
	keyPair, found := u.keystore[u.user]
	if !found {
		return 0, fmt.Errorf("no keys for user %v", u.user)
	}
	if len(keyPair.private) == 0 {
		return 0, fmt.Errorf("no private key for user %v", u.user)
	}

	// Decrypt our wrapped key.
	log.Printf("Keys found for user %v, private: %v", u.user, keyPair.private)
	aesKey := u.xor(wrapped.Wrapped, keyPair.private)
	log.Printf("AES Key after unwrapping: %s", aesKey)

	// Decrypt the ciphertext
	buf := u.xor(ciphertext, aesKey)
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
	ownerKeyPair, found := u.keystore[fileOwner]
	if !found {
		return len(buf), errors.New("file owner's key pair not found")
	}

	sig := sign(cleartext, ownerKeyPair.public)
	if sig != clear.Signature {
		err := fmt.Sprintf("expected signature %v, got %v", clear.Signature, sig)
		log.Println(err)
		return len(buf), errors.New(err)
	}

	return len(buf), nil
}

// PackLen implements the Packer interface
func (u *UnsafePack) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

// UnpackLen implements the Packer interface
func (u *UnsafePack) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

// extractWrappedKeyFromMeta returns the AES key stored in the user's own
// wrappedKeys or an error if no such information is found in the
// metadata.
func (u *UnsafePack) extractWrappedKeyFromMeta(clearMeta *clearMeta) (wrappedKey, error) {
	var wrapped wrappedKey
	for _, wk := range clearMeta.WrappedKeys {
		if string(wk.User) == string(u.user) {
			log.Printf("Wrapped Keys found for user: %v", u.user)
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

func (u *UnsafePack) SetCurrentUser(user upspin.UserName) {
	u.user = user
}

// MakeUserKeys returns a pair of public and private keys for a user.
func (u *UnsafePack) MakeUserKeys(user upspin.UserName) userKeys {
	return userKeys{
		user:    user,
		public:  u.xor([]byte(user), []byte("key")),
		private: u.xor([]byte(user), []byte("key")),
	}
}

func (u *UnsafePack) AddUserKeys(user upspin.UserName, userKeys userKeys) {
	u.keystore[user] = userKeys
}

func (u *UnsafePack) xor(contents []byte, key []byte) []byte {
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
