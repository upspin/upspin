// Package eeclient implements an end-to-end-encrypted upspin.Client.
// Alice shares a file with Bob by picking a new random symmetric key, encrypting the file,
// wrapping the symmetric encryption key with Bob's public key, signing the file using
// her own elliptic curve private key, and sending the ciphertext and metadata to a
// directory server.
package eeclient

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"upspin.googlesource.com/upspin.git/upspin"
)

var _ upspin.Client = (*Client)(nil)

// Client is a first-draft implementation of upspin.Client to illustrate ee crypto design.
// For Put, this represents the file creator's identity and crypto preferences.
// For Get, this contains enough to unwrap the file encryption key.
type Client struct {
	name      upspin.UserName  // which identity I wish to act as
	home      upspin.Directory // my trusted home directory server
	store     upspin.Store     // TODO  let's save that in home instead
	packing   upspin.Packing   // select which Client;  for now, assume EndToEnd
	factotum  string           // where to do private key operations; maybe a chan?
	keyserver string           // where to lookup public keys;  maybe a chan?
}

func New(name upspin.UserName, home upspin.Directory) *Client {
	return &Client{
		name:      name,
		home:      home,
		packing:   upspin.EndToEnd,
		factotum:  "factotum",  // placeholder for handle to ssh-agent
		keyserver: "keyserver", // placeholder for handle to e2email keyserver
	}
}

const (
	wrapPrefix = "wrap:" // for debugging draft;  should never appear in production
	aesKeylen  = 16
)

var (
	loc0 upspin.Location
)

// Signature and WrappedKeys are saved in upspin.Metadata when Location.Reference.Packing == EndToEnd
type Signature [sha256.Size]byte // TODO should be ECDSA
type WrappedKeys []WrappedKey

// A WrappedKey holds a key that will decrypt the file contents. The key is in turn
// encrypted with some user's private key.  A 16-bit hash of the user's public
// key is stored alongside to make it easier to find which key to use if many are
// present.
type WrappedKey struct {
	Hash      [2]byte // 16-bit hash of public key for user.
	Encrypted []byte  // Data decryption key, itself encrypted with public key for user.
}

func pdMarshal(sig Signature, wrap []WrappedKey) []byte {
	panic("unimplemented")
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, sig)
	if err != nil {
		fmt.Println("binary.Write sig failed")
	}
	err = binary.Write(buf, binary.BigEndian, wrap)
	if err != nil {
		fmt.Println("binary.Write wrap failed")
	}
	return buf.Bytes()
}

func pdUnmarshal(pd []byte) (sig Signature, wrap []WrappedKey) {
	panic("unimplemented")
}

func (c *Client) Get(name upspin.PathName) ([]byte, error) {
	var mismatch error
	dkey := make([]byte, aesKeylen)

	entry, err := c.home.Lookup(name)
	if err != nil {
		return nil, err
	}
	// TODO:  where loop over locations, as in testclient.go
	blob, _, err := c.store.Get(entry.Location)
	if blob == nil || err != nil {
		return nil, err
	}
	sig, wrap := pdUnmarshal(entry.Metadata.PackData)
	mykey := c.keyserver + "/" + string(c.name) // TODO replace by keyserver.Lookup
	hash16 := cksum(mykey)
	for _, w := range wrap {
		if hash16 != w.Hash {
			continue
		}
		if string(w.Encrypted[0:len(wrapPrefix)]) != wrapPrefix {
			return nil, fmt.Errorf("malformed wrapped key: %s", w.Encrypted)
		}
		// TODO   why encoding/hex instead of %X with Print and Scan?
		n, err := hex.Decode(dkey, w.Encrypted[len(wrapPrefix):]) // TODO  call factotum
		if err != nil || n != aesKeylen {
			return nil, fmt.Errorf("malformed wrapped hex: %s", w.Encrypted)
		}
		mess := []byte(fmt.Sprintf("%2x:%s:%x:%x", c.packing, name, dkey, blob))
		sum := sha256.Sum256(mess)
		if sum != sig {
			// TODO verify ECDSA; just use hash for now as sanity check
			mismatch = fmt.Errorf("retrieval %x does not verify: %s != %s", hash16, sum, sig)
			continue // maybe one of the other keys will work
		}
		return decrypt(dkey, blob)
	}
	if mismatch != nil {
		return nil, mismatch
	}
	return nil, fmt.Errorf("no wrapped key for me")
}

func (c *Client) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	dkey := make([]byte, 16)
	_, err := rand.Read(dkey)
	if err != nil {
		return loc0, err
	}
	blob, err := encrypt(dkey, data)
	if err != nil {
		return loc0, err
	}
	mess := []byte(fmt.Sprintf("%2x:%s:%x:%s", c.packing, name, dkey, blob))
	sig := Signature(sha256.Sum256(mess)) // TODO should be ECDSA using factotum
	usernames := []string{string(c.name)} // TODO should be readers of directory
	wrap := make([]WrappedKey, len(usernames))
	for i, user := range usernames {
		hash16 := cksum(user)                                // TODO call c.keyserver
		key := []byte(wrapPrefix + hex.EncodeToString(dkey)) // TODO rfc6637 §8 using factotum
		wrap[i] = WrappedKey{hash16, key}
	}
	return c.home.Put(name, blob, pdMarshal(sig, wrap))
}

func (c *Client) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	return c.home.MakeDirectory(dirName)
}

func encrypt(dkey, data []byte) ([]byte, error) {
	// AES 128 bit, len(dkey)==16
	block, err := aes.NewCipher(dkey)
	if err != nil {
		panic(err)
	}
	iv := make([]byte, aes.BlockSize)
	// iv=0 is ok because we're CERTAIN that dkey is random and not reused
	stream := cipher.NewCTR(block, iv)
	blob := make([]byte, len(data))
	stream.XORKeyStream(blob, data)
	return blob, nil
}

func decrypt(dkey, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(dkey)
	if err != nil {
		panic(err)
	}
	iv := make([]byte, aes.BlockSize)
	stream := cipher.NewCTR(block, iv)
	data := make([]byte, len(blob))
	stream.XORKeyStream(data, blob)
	return data, nil
}

func cksum(s string) (hash16 [2]byte) {
	// TODO is there already something lying around we can use?
	var h uint16
	for _, c := range s {
		h += uint16(c)
	}
	hash16[0] = byte(h >> 8)
	hash16[1] = byte(h & 0xff)
	return hash16
}
