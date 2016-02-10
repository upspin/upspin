// Package ee implements elliptic-curve end-to-end-encrypted packers.
// The first version, EEp256Pack, uses curve P256, SHA256, and AES-CTR 128;
// future versions, sharing code, will implement P386 and AES 256.
// Alice shares a file with Bob by picking a new random symmetric key, encrypting the file,
// wrapping the symmetric encryption key with Bob's public key, signing the file using
// her own elliptic curve private key, and sending the ciphertext and metadata to a
// directory server.
package ee

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	// "crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

type eePack struct {
	// removed factotum and keyserver links in favor of
	// SSH_AUTH_SOCK environment variable and
	// keyserver.Domainname(parsed.User) DNS record
}

var _ upspin.Packer = eePack{}

func init() {
	pack.Register(eePack{})
}

var errTooShort = errors.New("EEPack: destination slice too short")
var errMetaNil = errors.New("EEPack: caller must allocate Metadata struct")

const (
	wrapPrefix = "wrap:" // for debugging draft;  should never appear in production
	aesKeylen  = 16
)

// Signature sha256 as a placeholder   TODO switch to pkg crypto/ecdsa
// type Signature struct { r, s *big.Int }
type Signature [sha256.Size]byte

// WrappedKey holds a key that will decrypt file contents. The key is itself
// encrypted with user's private key.  A 16-bit hash of the user's public
// key is stored alongside to make it easier to find which key to use if many are
// present.
type WrappedKey struct {
	Hash      byte   // 8-bit xor of public key for user.
	Encrypted []byte // Data decryption key, itself encrypted with public key for user.
}
type WrappedKeys []WrappedKey

func (eePack) Packing() upspin.Packing {
	return upspin.EEp256Pack
}

func (e eePack) Pack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	if meta == nil {
		return 0, errMetaNil
	}
	dkey := make([]byte, aesKeylen)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, err
	}
	n, err := encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, err
	}

	parsed, err := path.Parse(name) // first wrapped key is for owner = parsed.User
	if err != nil {
		return 0, fmt.Errorf("EEPack: filename does not parse: %s: %v", parsed.User, err)
	}
	usernames := []string{string(parsed.User)} // TODO should be readers of directory

	mess := []byte(fmt.Sprintf("%2x:%s:%x:%q", e.Packing(), name, dkey, ciphertext))
	sig := Signature(sha256.Sum256(mess)) // TODO should be ECDSA, but need factotum implementation first
	// priv := &ecdsa.PrivateKey{}
	// r, s, err := ecdsa.Sign(rand.Reader, priv, sha256.Sum256(mess))
	// if err != nil {
	//	return 0, fmt.Errorf("EEPack: ECDSA failed, %v", err)
	// }
	// sig := Signature{r, s}
	wrap := make([]WrappedKey, len(usernames))
	for i, user := range usernames {
		hash8 := xorBytes(user)                              // TODO call keyserver, cksum(publickey(user))
		key := []byte(wrapPrefix + hex.EncodeToString(dkey)) // TODO rfc6637 §8 using factotum
		wrap[i] = WrappedKey{hash8, key}
	}

	meta.PackData = pdMarshal(sig, wrap)
	return n, nil
}

func (e eePack) Unpack(cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	if meta == nil {
		return 0, errMetaNil
	}
	var mismatch error
	dkey := make([]byte, aesKeylen)
	sig, wrap := pdUnmarshal(meta.PackData, name)
	parsed, err := path.Parse(name) // first wrapped key is for owner = parsed.User
	if err != nil {
		return 0, fmt.Errorf("EEPack: filename does not parse: %s", parsed.User)
	}
	mykey := "keyserver/" + string(parsed.User) // TODO replace by keyserver.Lookup
	hash8 := xorBytes(mykey)
	for _, w := range wrap {
		if hash8 != w.Hash {
			continue
		}
		if string(w.Encrypted[0:len(wrapPrefix)]) != wrapPrefix {
			return 0, fmt.Errorf("malformed wrapped key: %s", w.Encrypted)
		}
		// TODO   why encoding/hex instead of %X with Print and Scan?
		n, err := hex.Decode(dkey, w.Encrypted[len(wrapPrefix):]) // TODO  call factotum
		if err != nil || n != aesKeylen {
			return 0, fmt.Errorf("malformed wrapped hex: %s", w.Encrypted)
		}
		mess := []byte(fmt.Sprintf("%2x:%s:%x:%x", e.Packing(), name, dkey, ciphertext))
		sum := sha256.Sum256(mess)
		if sum != sig {
			// TODO verify ECDSA; just use hash for now as sanity check
			// if !ecdsa.Verify(pub, sum, sig.r, sig.s) {
			//	mismatch = fmt.Errorf("retrieval %x does not verify", hash8)
			// }
			mismatch = fmt.Errorf("retrieval %x does not verify: %s != %s", hash8, sum, sig)
			continue // maybe one of the other keys will work
		}
		return decrypt(cleartext, ciphertext, dkey)
	}
	if mismatch != nil {
		return 0, mismatch
	}
	return 0, fmt.Errorf("no wrapped key for me")
}

func (eePack) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (eePack) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (eePack) String() string {
	return "ee" // TODO what is supposed to go here?
}

func pdMarshal(sig Signature, wrap []WrappedKey) []byte {
	buf := new(bytes.Buffer)
	// TODO   ECDSA
	// err := binary.Write(buf, binary.BigEndian, sig.r)
	// err := binary.Write(buf, binary.BigEndian, sig.s)
	err := binary.Write(buf, binary.BigEndian, sig)
	if err != nil {
		fmt.Println("binary.Write sig failed: %s", err)
		return nil
	}
	err = binary.Write(buf, binary.BigEndian, uint64(len(wrap)))
	if err != nil {
		fmt.Println("binary.Write len(wrap) failed: %s", err)
		return nil
	}
	for i, w := range wrap {
		err = binary.Write(buf, binary.BigEndian, w.Hash)
		if err != nil {
			fmt.Println("binary.Write wrap[%d].Hash failed: %s", i, err)
			return nil
		}
		err = binary.Write(buf, binary.BigEndian, w.Encrypted)
		if err != nil {
			fmt.Println("binary.Write wrap[%d].Encrypted failed: %s", i, err)
			return nil
		}
	}
	return buf.Bytes()
}

func pdUnmarshal(pd []byte, name upspin.PathName) (sig Signature, wrap []WrappedKey) {
	buf := bytes.NewReader(pd)
	sigslice := make([]byte, len(sig))
	// TODO   ECDSA
	err := binary.Read(buf, binary.BigEndian, sigslice)
	if err != nil {
		fmt.Println("binary.Read failed:", err)
	}
	if len(sigslice) != len(sig) {
		fmt.Println("expected len(sig)=%d, got %d", len(sig), len(sigslice))
	}
	n := uint64(0)
	err = binary.Read(buf, binary.BigEndian, n)
	if err != nil {
		fmt.Println("binary.Read failed:", err)
	}
	wrap = make([]WrappedKey, n)
	for i := 0; i < int(n); i++ {
		err = binary.Read(buf, binary.BigEndian, wrap[i])
		if err != nil {
			fmt.Println("binary.Read failed:", err)
		}
	}
	return sig, wrap
}

func encrypt(ciphertext, cleartext, dkey []byte) (int, error) {
	if len(dkey) != aesKeylen {
		panic("unimplemented") // should recognize 32 as AES 256
	}
	block, err := aes.NewCipher(dkey)
	if err != nil {
		panic(err)
	}
	iv := make([]byte, aes.BlockSize)
	// iv=0 is ok because we're CERTAIN that dkey is random and not reused
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, cleartext)
	return len(cleartext), nil
}

func decrypt(cleartext, ciphertext, dkey []byte) (int, error) {
	block, err := aes.NewCipher(dkey)
	if err != nil {
		panic(err)
	}
	iv := make([]byte, aes.BlockSize)
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(cleartext, ciphertext)
	return len(ciphertext), nil
}

func xorBytes(data string) byte {
	parity := byte(0)
	for _, c := range []byte(data) {
		parity ^= c
	}
	return parity
}
