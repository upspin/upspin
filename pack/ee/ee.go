// Package ee implements elliptic-curve end-to-end-encrypted packers.
package ee

// Upspin crypto summary:
// Alice shares a file with Bob by picking a new random symmetric key, encrypting the file,
// wrapping the symmetric encryption key with Bob's public key, signing the file using
// her own elliptic curve private key, and sending the ciphertext and metadata to a
// directory server.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"strings"

	"golang.org/x/crypto/hkdf"
	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/key/keyloader"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// wrappedKey encodes a key that will decrypt and verify the ciphertext.
type wrappedKey struct {
	keyHash   []byte // sha256(recipient PublicKey)
	dkey      []byte // ciphertext symmetric decryption key, encrypted for recipient PublicKey
	nonce     []byte
	ephemeral ecdsa.PublicKey
}

type keyHashArray [sha256.Size]byte // sometimes we need the array

// common implements common functions parameterized by cipher-specific values.
type common struct {
	curve        elliptic.Curve
	packerString string
	packing      upspin.Packing
}

var _ upspin.Packer = common{}

type eep256 struct {
	common
}

type eep384 struct {
	common
}

type eep521 struct {
	common
}

const (
	aesKeyLen          = 32 // AES-256 because public cloud should withstand multifile multikey attack.
	p256               = "p256"
	p384               = "p384"
	p521               = "p521"
	noKnownKeysForUser = "no known keys for user %s"
)

func init() {
	pack.Register(eep256{
		common{
			curve:        elliptic.P256(),
			packerString: p256,
			packing:      upspin.EEp256Pack,
		},
	})
	pack.Register(eep384{
		common{
			curve:        elliptic.P384(),
			packerString: p384,
			packing:      upspin.EEp384Pack,
		},
	})
	pack.Register(eep521{
		common{
			curve:        elliptic.P521(),
			packerString: p521,
			packing:      upspin.EEp521Pack,
		},
	})
}

const (
	// unfortunately cipher/gcm.go doesn't export these
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

var (
	errTooShort     = errors.New("destination slice too short")
	errVerify       = errors.New("does not verify")
	errNoWrappedKey = errors.New("no wrapped key for me")
	errKeyLength    = errors.New("wrong key length")
	sig0            upspin.Signature // for returning nil of correct type
)

func (c common) Packing() upspin.Packing {
	return c.packing
}

func (c common) PackLen(ctx *upspin.Context, cleartext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckPackMeta(c, &d.Metadata); err != nil {
		return -1
	}
	return len(cleartext)
}

func (c common) UnpackLen(ctx *upspin.Context, ciphertext []byte, d *upspin.DirEntry) int {
	if err := pack.CheckUnpackMeta(c, &d.Metadata); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (c common) String() string {
	return c.packerString
}

func (c common) Pack(ctx *upspin.Context, ciphertext, cleartext []byte, d *upspin.DirEntry) (int, error) {
	if err := pack.CheckPackMeta(c, &d.Metadata); err != nil {
		return 0, err
	}
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	ciphertext = ciphertext[:len(cleartext)]

	// Pick fresh file encryption key.
	dkey := make([]byte, aesKeyLen)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, err
	}
	cipherLen, err := c.encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, err
	}
	b := sha256.Sum256(ciphertext)
	cipherSum := b[:]

	// Sign ciphertext.
	sig, err := ctx.Factotum.FileSign(ctx.Packing, d.Name, d.Metadata.Time, dkey, cipherSum)
	if err != nil {
		return 0, err
	}

	// Wrap for myself.
	// TODO Update this other readers, as soon as we can get the list.
	wrap := make([]wrappedKey, 1)
	p, err := parsePublicKey(ctx.KeyPair.Public, c.packerString)
	if err != nil {
		return 0, err
	}
	wrap[0], err = c.aesWrap(p, dkey)
	if err != nil {
		return 0, err
	}

	// Serialize packer metadata.
	err = c.pdMarshal(&d.Metadata.Packdata, sig, wrap, cipherSum)
	if err != nil {
		return 0, err
	}
	return cipherLen, err
}

func (c common) Unpack(ctx *upspin.Context, cleartext, ciphertext []byte, d *upspin.DirEntry) (int, error) {
	if err := pack.CheckUnpackMeta(c, &d.Metadata); err != nil {
		return 0, err
	}
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	cleartext = cleartext[:len(ciphertext)]

	// Retrieve file decryption key.
	dkey := make([]byte, aesKeyLen)
	sig, wrap, _, err := c.pdUnmarshal(d.Metadata.Packdata)
	if err != nil {
		return 0, err
	}

	// File owner is part of the pathname
	parsed, err := path.Parse(d.Name)
	owner := parsed.User
	if err != nil {
		return 0, err
	}
	// The owner has a well-known public key
	ownerRawPubKey, err := publicKey(ctx, owner, c.packerString)
	if err != nil {
		return 0, err
	}
	ownerPubKey, err := parsePublicKey(ownerRawPubKey, c.packerString)
	if err != nil {
		return 0, err
	}

	// Now get my own keys
	me := ctx.UserName // Recipient of the file is me (the user in the context)
	rawPublicKey, err := publicKey(ctx, me, c.packerString)
	if err != nil {
		return 0, err
	}
	pubkey, err := parsePublicKey(rawPublicKey, c.packerString)
	if err != nil {
		return 0, err
	}

	// For quick lookup, hash my public key and locate my wrapped key in the metadata.
	rhash := keyHash(pubkey)
	b := sha256.Sum256(ciphertext)
	cipherSum := b[:]
	for _, w := range wrap {
		if !bytes.Equal(rhash, w.keyHash) {
			continue
		}
		// Decode my wrapped key using my private key
		dkey, err = c.aesUnwrap(ctx.Factotum, w)
		if err != nil {
			log.Printf("unwrap failed: %v", err)
			return 0, err
		}
		// Verify that the owner signed this with his/her public key.
		if !ecdsa.Verify(ownerPubKey, auth.VerHash(ctx.Packing, d.Name, d.Metadata.Time, dkey, cipherSum), sig.R, sig.S) {
			log.Println("verify failed")
			return 0, errVerify
		}
		// dkey is safe, so we decrypt the whole blob.
		return c.decrypt(cleartext, ciphertext, dkey)
	}
	return 0, errNoWrappedKey
}

// Share extracts dkey from the packdata, wraps for readers, and updates packdata.
func (c common) Share(ctx *upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {

	// Fetch all the public keys we'll need.
	pubkey := make([]*ecdsa.PublicKey, len(readers))
	hash := make([]keyHashArray, len(readers))
	for i, pub := range readers {
		// TODO(ehg) someday deal with diverse key types amongst readers
		var err error
		pubkey[i], err = parsePublicKey(pub, c.packerString)
		if err != nil {
			continue
		}
		copy(hash[i][:], keyHash(pubkey[i]))
	}

	// Get my own key.
	mypub, err := parsePublicKey(ctx.KeyPair.Public, c.packerString)
	if err != nil {
		log.Printf("cannot parse my own key: %v", err)
		return // can't happen
	}
	myhash := keyHash(mypub)

	// For each packdata, wrap for new readers.
	for i, d := range packdata {

		// Extract dkey and existing wrapped keys from packdata.
		var dkey []byte
		alreadyWrapped := make(map[keyHashArray]wrappedKey)
		sig, wrap, cipherSum, err := c.pdUnmarshal(*d)
		for i, w := range wrap {
			var h keyHashArray
			copy(h[:], w.keyHash)
			alreadyWrapped[h] = wrap[i]
			if !bytes.Equal(myhash, w.keyHash) {
				continue
			}
			dkey, err = c.aesUnwrap(ctx.Factotum, w)
			if err != nil {
				log.Printf("dkey unwrap failed: %v", err)
				break // give up;  might mean that owner has changed keys
			}
		}
		packdata[i] = nil
		if len(dkey) == 0 {
			continue // failed to get dkey
		}

		// Create new list of wrapped keys.
		wrap = make([]wrappedKey, len(readers))
		nwrap := 0
		for i := range readers {
			if pubkey[i] == nil {
				continue
			}
			w, ok := alreadyWrapped[hash[i]]
			if !ok { // then need to wrap
				w, err = c.aesWrap(pubkey[i], dkey)
				if err != nil {
					continue
				}
				v := w.ephemeral
				log.Printf("Wrap for %x [%d %d]", hash[i], v.X, v.Y)
			} // else reuse the existing wrapped key
			wrap[nwrap] = w
			nwrap++
		}
		wrap = wrap[:nwrap]

		// Rebuild packdata[i] from existing sig and new wrapped keys.
		dst := make([]byte, c.packdataLen(nwrap))
		err = c.pdMarshal(&dst, sig, wrap, cipherSum)
		if err != nil {
			continue
		}
		packdata[i] = &dst
	}
}

// Name implements upspin.Name.
func (c common) Name(ctx *upspin.Context, d *upspin.DirEntry, newName upspin.PathName) error {
	if err := pack.CheckUnpackMeta(c, &d.Metadata); err != nil {
		return err
	}

	dkey := make([]byte, aesKeyLen)
	sig, wrap, cipherSum, err := c.pdUnmarshal(d.Metadata.Packdata)
	if err != nil {
		return err
	}
	// TODO(p): take this out when the sum is mandatory since the check will happen in pdUnmarshal
	if cipherSum == nil {
		return errTooShort
	}

	// File owner is part of the pathname
	parsed, err := path.Parse(d.Name)
	owner := parsed.User
	if err != nil {
		return err
	}
	// The owner has a well-known public key
	ownerRawPubKey, err := publicKey(ctx, owner, c.packerString)
	if err != nil {
		return err
	}
	ownerPubKey, err := parsePublicKey(ownerRawPubKey, c.packerString)
	if err != nil {
		return err
	}

	// Now get my own keys
	me := ctx.UserName // Recipient of the file is me (the user in the context)
	rawPublicKey, err := publicKey(ctx, me, c.packerString)
	if err != nil {
		return err
	}
	pubkey, err := parsePublicKey(rawPublicKey, c.packerString)
	if err != nil {
		return err
	}

	// For quick lookup, hash my public key and locate my wrapped key in the metadata.
	rhash := keyHash(pubkey)
	wrapFound := false
	var w wrappedKey
	for _, w = range wrap {
		if bytes.Equal(rhash, w.keyHash) {
			wrapFound = true
			break
		}
	}
	if !wrapFound {
		log.Printf("unwrap failed: %s", errNoWrappedKey)
		return errNoWrappedKey
	}

	// Decode my wrapped key using my private key
	dkey, err = c.aesUnwrap(ctx.Factotum, w)
	if err != nil {
		log.Printf("unwrap failed: %s", err)
		return err
	}

	// Verify that the owner signed this with his/her public key.
	if !ecdsa.Verify(ownerPubKey, auth.VerHash(ctx.Packing, d.Name, d.Metadata.Time, dkey, cipherSum), sig.R, sig.S) {
		log.Println("verify failed")
		return errVerify
	}

	// If we are changing directories, remove all wrapped keys except my own.
	parsedNew, err := path.Parse(newName)
	if err != nil {
		return err
	}
	if !parsed.Drop(1).Equal(parsedNew.Drop(1)) {
		wrap = []wrappedKey{w}
	}

	// Compute new signature.
	sig, err = ctx.Factotum.FileSign(ctx.Packing, newName, d.Metadata.Time, dkey, cipherSum)
	if err != nil {
		return err
	}

	// Serialize packer metadata. We do not reallocate Packdata since the new data
	// should be the same size or smaller.
	if err := c.pdMarshal(&d.Metadata.Packdata, sig, wrap, cipherSum); err != nil {
		return err
	}
	d.Name = newName

	return nil
}

func packname(curve elliptic.Curve) string {
	switch curve {
	case elliptic.P256():
		return p256
	case elliptic.P384():
		return p384
	case elliptic.P521():
		return p521
	default:
		return "unknownPacking"
	}
}

func keyHash(p *ecdsa.PublicKey) []byte {
	keyBytes := []byte(fmt.Sprintf("%s\n%s\n%s\n", packname(p.Curve), p.X.String(), p.Y.String()))
	// this string should be the same as the file contents ~/.ssh/public.upspinkey
	keyHash := sha256.Sum256(keyBytes)
	return keyHash[:]
}

// aesWrap implements NIST 800-56Ar2; see also RFC6637 §8.
func (c common) aesWrap(R *ecdsa.PublicKey, dkey []byte) (w wrappedKey, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// v, V=vG  ephemeral key pair
	// S = vR   shared point
	v, err := ecdsa.GenerateKey(c.curve, rand.Reader)
	sx, sy := c.curve.ScalarMult(R.X, R.Y, v.D.Bytes())
	S := elliptic.Marshal(c.curve, sx, sy)
	w.ephemeral = ecdsa.PublicKey{Curve: c.curve, X: v.X, Y: v.Y}

	// Step 2.  Convert shared secret to strong secret via HKDF.
	w.nonce = make([]byte, gcmStandardNonceSize)
	_, err = rand.Read(w.nonce)
	if err != nil {
		return
	}
	w.keyHash = keyHash(R)
	mess := []byte(fmt.Sprintf("%02x:%x:%x", c.packing, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess) // TODO(security-reviewer) reconsider salt
	strong := make([]byte, aesKeyLen)
	_, err = io.ReadFull(hkdf, strong)
	if err != nil {
		return
	}

	// Step 3. Encrypt dkey.
	block, err := aes.NewCipher(strong)
	if err != nil {
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return
	}
	w.dkey = make([]byte, 0, len(dkey)+gcmTagSize)
	w.dkey = aead.Seal(w.dkey, w.nonce, dkey, nil)
	// TODO(ehg) figure out why aead.Seal allocated memory here
	return
}

// Extract per-file symmetric key from w.
// If error, len(dkey)==0.
func (c common) aesUnwrap(f upspin.Factotum, w wrappedKey) (dkey []byte, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// S = rV
	sx, sy := f.ScalarMult(c.curve, w.ephemeral.X, w.ephemeral.Y)
	S := elliptic.Marshal(c.curve, sx, sy)

	// Step 2.  Convert shared secret to strong secret via HKDF.
	mess := []byte(fmt.Sprintf("%02x:%x:%x", c.packing, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess)
	strong := make([]byte, aesKeyLen)
	_, err = io.ReadFull(hkdf, strong)
	if err != nil {
		log.Printf("Error reading from hkdf: %v", err)
		return
	}

	// Step 3. Decrypt dkey.
	block, err := aes.NewCipher(strong)
	if err != nil {
		log.Printf("Error in creating new cipher block: %v", err)
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("Error in creating new GCM block: %v", err)
		return
	}
	dkey = make([]byte, 0, aesKeyLen)
	dkey, err = aead.Open(dkey, w.nonce, w.dkey, nil)
	if err != nil {
		dkey = dkey[:0]
	}
	return
}

func (c common) pdMarshal(dst *[]byte, sig upspin.Signature, wrap []wrappedKey, cipherSum []byte) error {
	n := c.packdataLen(len(wrap))
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	(*dst)[0] = byte(c.packing)
	n = 1
	n += pdPutBytes((*dst)[n:], sig.R.Bytes())
	n += pdPutBytes((*dst)[n:], sig.S.Bytes())
	n += binary.PutVarint((*dst)[n:], int64(len(wrap)))
	for _, w := range wrap {
		n += pdPutBytes((*dst)[n:], w.keyHash)
		n += pdPutBytes((*dst)[n:], w.dkey)
		n += pdPutBytes((*dst)[n:], w.nonce)
		n += pdPutBytes((*dst)[n:], w.ephemeral.X.Bytes())
		n += pdPutBytes((*dst)[n:], w.ephemeral.Y.Bytes())
	}
	// TODO(p): eventually make hash mandatory.  Currently not for backward compatability.
	if cipherSum != nil {
		n += pdPutBytes((*dst)[n:], cipherSum)
	}
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
}

func (c common) pdUnmarshal(pd []byte) (sig upspin.Signature, wrap []wrappedKey, hash []byte, err error) {
	if pd[0] != byte(c.packing) {
		return sig0, nil, nil, fmt.Errorf("expected packing %d, got %d", c.packing, pd[0])
	}
	n := 1
	sig.R = big.NewInt(0)
	sig.S = big.NewInt(0)
	byteLen := (c.curve.Params().BitSize + 7) >> 3
	buf := make([]byte, byteLen)
	n += pdGetBytes(&buf, pd[n:])
	sig.R.SetBytes(buf)
	n += pdGetBytes(&buf, pd[n:])
	sig.S.SetBytes(buf)
	nwrap64, vlen := binary.Varint(pd[n:])
	n += vlen
	nwrap := int(nwrap64)
	if int64(nwrap) != nwrap64 {
		return sig0, nil, nil, fmt.Errorf("implausible number of wrapped keys: %d\n", nwrap64)
	}
	wrap = make([]wrappedKey, nwrap)
	for i := 0; i < nwrap; i++ {
		var w wrappedKey
		w.keyHash = make([]byte, sha256.Size)
		w.dkey = make([]byte, aesKeyLen+gcmTagSize)
		w.nonce = make([]byte, gcmStandardNonceSize)
		w.ephemeral = ecdsa.PublicKey{Curve: c.curve, X: big.NewInt(0), Y: big.NewInt(0)}
		n += pdGetBytes(&w.keyHash, pd[n:])
		n += pdGetBytes(&w.dkey, pd[n:])
		n += pdGetBytes(&w.nonce, pd[n:])
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.X.SetBytes(buf)
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.Y.SetBytes(buf)
		wrap[i] = w
	}
	// TODO(p): eventually make hash mandatory.  Currently not for backward compatability.
	// The +1 is for the varint size preceding the hash.
	if len(pd)-n == sha256.Size+1 {
		hash = make([]byte, sha256.Size)
		n += pdGetBytes(&hash, pd[n:])
	}
	if n != len(pd) { // sanity check, not a thorough parser test
		return sig0, nil, nil, fmt.Errorf("got %d, expected %d", n, len(pd))
	}
	return sig, wrap, hash, nil
}

// pdPutBytes puts length header in dst and then copies src to dst; returns bytes consumed
func pdPutBytes(dst, src []byte) int {
	vlen := binary.PutVarint(dst, int64(len(src)))
	if vlen <= 0 {
		panic("PutVarint")
	}
	k := copy(dst[vlen:], src)
	if k != len(src) {
		panic("can't happen")
	}
	return vlen + k
}

// pdBytes copies (part of) src to dst, based on length header; returns bytes consumed
func pdGetBytes(dst *[]byte, src []byte) int {
	n, vlen := binary.Varint(src)
	if vlen <= 0 {
		panic("Varint")
	}
	*dst = (*dst)[:n]
	k := copy(*dst, src[vlen:n+int64(vlen)])
	if int64(k) != n {
		// can't happen unless dst too short?
		*dst = (*dst)[:0]
		return k + vlen
	}
	return k + vlen
}

func (c common) encrypt(ciphertext, cleartext, dkey []byte) (int, error) {
	if len(dkey) != aesKeyLen {
		return 0, errKeyLength
	}
	block, err := aes.NewCipher(dkey)
	if err != nil {
		return 0, err
	}
	iv := make([]byte, aes.BlockSize)
	// iv=0 is ok because we're CERTAIN that dkey is random and not reused
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, cleartext)
	return len(cleartext), nil
}

func (c common) decrypt(cleartext, ciphertext, dkey []byte) (int, error) {
	if len(dkey) != aesKeyLen {
		return 0, errKeyLength
	}
	block, err := aes.NewCipher(dkey)
	if err != nil {
		return 0, err
	}
	iv := make([]byte, aes.BlockSize)
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(cleartext, ciphertext)
	return len(ciphertext), nil
}

// packdataLen returns n big enough for packing, sig.R, sig.S, nwrap, {keyHash, encrypted, nonce, X, y}
func (c common) packdataLen(nwrap int) int {
	// byteLen is copied from elliptic.go:Marshal()
	byteLen := (c.curve.Params().BitSize + 7) >> 3
	return 1 + 2*byteLen + (1+5*nwrap)*binary.MaxVarintLen64 +
		nwrap*(sha256.Size+(aesKeyLen+gcmTagSize)+gcmStandardNonceSize+2*byteLen) +
		sha256.Size + 1
}

// publicKey returns the string representation of a user's public key.
func publicKey(ctx *upspin.Context, user upspin.UserName, packerString string) (upspin.PublicKey, error) {

	// KeyPairs have three representations:
	// 1. string, used for storage and between programs like User.Lookup
	// 2. ecdsa, internal binary format for computation
	// 3. a secret seed sufficient to reconstruct the key pair
	// In form 1, the first bytes describe the packing name, e.g. "p256".
	// In form 2, there is an Curve field in the struct that plays that role.
	// Form 3, used only in keygen.go, is simply 128 bits of entropy.

	log.Printf("Getting pub key for user: %s", user)
	// Are we requesting our own public key?
	if string(user) == string(ctx.UserName) {
		return ctx.KeyPair.Public, nil
	}
	_, keys, err := ctx.User.Lookup(user)
	if err != nil {
		return "", err
	}
	if len(keys) < 1 {
		return "", fmt.Errorf(noKnownKeysForUser, user)
	}
	for _, k := range keys {
		if IsValidKeyForPacker(k, packerString) {
			return k, nil
		}
	}
	return "", fmt.Errorf(noKnownKeysForUser, user)
}

// parsePublicKey takes a string representation of a
// public key and converts it into an ECDSA public key.
func parsePublicKey(publicKey upspin.PublicKey, packerString string) (*ecdsa.PublicKey, error) {
	ecdsaPubKey, keyType, err := keyloader.ParsePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	if keyType != packerString {
		return nil, fmt.Errorf("expected packing %s, got %s", packerString, keyType)
	}
	return ecdsaPubKey, nil
}

// IsValidKeyForPacker returns true if key is used for the specified packing.
func IsValidKeyForPacker(publicKey upspin.PublicKey, packerString string) bool {
	return strings.HasPrefix(string(publicKey), packerString)
}
