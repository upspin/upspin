// Package ee implements elliptic-curve end-to-end-encrypted packers.

// Upspin crypto summary:
// Alice shares a file with Bob by picking a new random symmetric key, encrypting the file,
// wrapping the symmetric encryption key with Bob's public key, signing the file using
// her own elliptic curve private key, and sending the ciphertext and metadata to a
// directory server.
package ee

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
	"upspin.googlesource.com/upspin.git/key/keyloader"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// signature is an ECDSA signature
type signature struct {
	r *big.Int
	s *big.Int
}

// wrappedKey encodes a key that will decrypt and verify the ciphertext.
type wrappedKey struct {
	keyHash   []byte // sha256(recipient PublicKey)
	encrypted []byte // ciphertext key, encrypted for recipient PublicKey
	nonce     []byte
	ephemeral ecdsa.PublicKey
}
type wrappedKeys []wrappedKey

// common implements common functions parameterized by cipher-specific values.
type common struct {
	ciphersuite  upspin.Packing
	curve        elliptic.Curve
	aesLen       int
	packerString string
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
	p256               = "p256"
	p384               = "p384"
	p521               = "p521"
	noKnownKeysForUser = "no known keys for user %s"
)

func init() {
	pack.Register(eep256{
		common{
			ciphersuite:  upspin.EEp256Pack,
			curve:        elliptic.P256(),
			aesLen:       16,
			packerString: p256,
		},
	})
	pack.Register(eep384{
		common{
			ciphersuite:  upspin.EEp384Pack,
			curve:        elliptic.P384(),
			aesLen:       32,
			packerString: p384,
		},
	})
	pack.Register(eep521{
		common{
			ciphersuite:  upspin.EEp521Pack,
			curve:        elliptic.P521(),
			aesLen:       32,
			packerString: p521,
		},
	})
}

const (
	// TODO unfortunately cipher/gcm.go doesn't export these
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

var (
	errTooShort     = errors.New("destination slice too short")
	errVerify       = errors.New("does not verify")
	errNoWrappedKey = errors.New("no wrapped key for me")
	errKeyLength    = errors.New("wrong key length")
	sig0            signature // for returning nil of correct type
)

func (e common) Packing() upspin.Packing {
	return e.ciphersuite
}

func (e common) PackLen(ctx *upspin.Context, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	if err := pack.CheckPackMeta(e, meta); err != nil {
		return -1
	}
	return len(cleartext)
}

func (e common) UnpackLen(ctx *upspin.Context, ciphertext []byte, meta *upspin.Metadata) int {
	if err := pack.CheckUnpackMeta(e, meta); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (e common) String() string {
	return e.packerString
}

func (e common) Pack(ctx *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckPackMeta(e, meta); err != nil {
		return 0, err
	}
	return e.eePack(ctx, ciphertext, cleartext, meta, name)
}

func (e common) Unpack(ctx *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckUnpackMeta(e, meta); err != nil {
		return 0, err
	}
	return e.eeUnpack(ctx, cleartext, ciphertext, meta, name)
}

func (c common) eePack(ctx *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, pathname upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	ciphertext = ciphertext[:len(cleartext)]
	dkey := make([]byte, c.aesLen)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, err
	}
	nCipher, err := c.encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, err
	}

	// Set up readers. The writer of a file is always a reader.
	usernames := append([]upspin.UserName{ctx.UserName}, meta.Readers...)
	myRawPublicKey, err := c.publicKey(ctx, ctx.UserName)
	if err != nil {
		return 0, err
	}
	myPublicKey, err := c.parsePublicKey(myRawPublicKey)
	if err != nil {
		return 0, err
	}
	myPrivateKey, err := c.parsePrivateKey(myPublicKey, ctx.KeyPair)
	if err != nil {
		return 0, err
	}

	r, s, err := ecdsa.Sign(rand.Reader, myPrivateKey, c.verHash(pathname, dkey, ciphertext))
	if err != nil {
		return 0, err
	}
	sig := signature{r, s}
	var firstErr error
	wrap := make([]wrappedKey, len(usernames))
	nwrap := 0
	for _, u := range usernames {
		readerRawPublicKey, err := c.publicKey(ctx, u)
		if err != nil {
			log.Printf("no public key found for user %s: %s", u, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		readerPublicKey, err := c.parsePublicKey(readerRawPublicKey)
		if err != nil {
			log.Printf("parsing public key for user %s: %s", u, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		log.Printf("Wrapping key for user %s", u)
		wrap[nwrap], err = c.aesWrap(readerPublicKey, myPrivateKey, dkey)
		if err != nil {
			return 0, err
		}
		nwrap++
	}
	wrap = wrap[:nwrap]
	err = c.pdMarshal(&meta.PackData, sig, wrap)
	if err != nil {
		return 0, err
	}
	return nCipher, firstErr
}

func (c common) eeUnpack(ctx *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, pathname upspin.PathName) (int, error) {
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	cleartext = cleartext[:len(ciphertext)]
	dkey := make([]byte, c.aesLen)
	sig, wrap, err := c.pdUnmarshal(meta.PackData, pathname)
	if err != nil {
		return 0, err
	}

	// File owner is part of the pathname
	parsed, err := path.Parse(pathname)
	owner := parsed.User
	if err != nil {
		return 0, err
	}
	// The owner has a well-known public key
	ownerRawPubKey, err := c.publicKey(ctx, owner)
	if err != nil {
		return 0, err
	}
	ownerPubKey, err := c.parsePublicKey(ownerRawPubKey)
	if err != nil {
		return 0, err
	}

	// Now get my own keys
	me := ctx.UserName // Recipient of the file is me (the user in the context)
	rawPublicKey, err := c.publicKey(ctx, me)
	if err != nil {
		return 0, err
	}
	pubkey, err := c.parsePublicKey(rawPublicKey)
	if err != nil {
		return 0, err
	}
	privateKey, err := c.parsePrivateKey(pubkey, ctx.KeyPair)
	if err != nil {
		return 0, err
	}
	// For quick lookup, hash my public key and locate my wrapped
	// key in the metadata.
	rhash := c.keyHash(pubkey)
	for _, w := range wrap {
		if !bytes.Equal(rhash, w.keyHash) {
			log.Printf("unequal %x\n        %x\n", rhash, w.keyHash)
			continue
		}
		// Decode my wrapped key using my private key
		dkey, err = c.aesUnwrap(privateKey, w)
		if err != nil {
			log.Printf("unwrap failed: %v", err)
			return 0, err
		}
		// Verify that the owner signed this with his/her public key.
		if !ecdsa.Verify(ownerPubKey, c.verHash(pathname, dkey, ciphertext), sig.r, sig.s) {
			log.Println("verify failed")
			return 0, errVerify
		}
		// dkey is safe, so we decrypt the whole blob.
		return c.decrypt(cleartext, ciphertext, dkey)
	}
	return 0, errNoWrappedKey
}

func (c common) verHash(pathname upspin.PathName, dkey, ciphertext []byte) []byte {
	// TODO Consider alternative crypto that merges verification with wrapping.
	// TODO If we stick with Sign, consider streaming ciphertext to sha256 here.
	mess := []byte(fmt.Sprintf("%02x:%s:%x:%x", c.ciphersuite, pathname, dkey, ciphertext))
	messhash := sha256.Sum256(mess)
	return messhash[:]
}

func (c common) keyHash(p *ecdsa.PublicKey) []byte {
	keybytes := []byte(fmt.Sprintf("%s\n%s\n%s\n", c.ciphersuite, p.X.String(), p.Y.String()))
	// this string should be the same as the file contents ~/.ssh/public.upspinkey
	// TODO change to new KeyID
	keyHash := sha256.Sum256(keybytes)
	return keyHash[:]
}

// aesWrap implements NIST 800-56Ar2; see also RFC6637 §8.
func (c common) aesWrap(R *ecdsa.PublicKey, own *ecdsa.PrivateKey, dkey []byte) (w wrappedKey, err error) {
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
	w.keyHash = c.keyHash(R)
	mess := []byte(fmt.Sprintf("%02x:%x:%x", c.ciphersuite, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess) // TODO reconsider salt
	strong := make([]byte, c.aesLen)
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
	w.encrypted = make([]byte, 0, len(dkey)+gcmTagSize)
	w.encrypted = aead.Seal(w.encrypted, w.nonce, dkey, nil)
	// TODO figure out why aead.Seal allocated memory here
	return
}

func (c common) aesUnwrap(R *ecdsa.PrivateKey, w wrappedKey) (dkey []byte, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// S = rV
	sx, sy := c.curve.ScalarMult(w.ephemeral.X, w.ephemeral.Y, R.D.Bytes())
	S := elliptic.Marshal(c.curve, sx, sy)

	// Step 2.  Convert shared secret to strong secret via HKDF.
	mess := []byte(fmt.Sprintf("%02x:%x:%x", c.ciphersuite, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess)
	strong := make([]byte, c.aesLen)
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
	dkey = make([]byte, 0, c.aesLen)
	dkey, err = aead.Open(dkey, w.nonce, w.encrypted, nil)
	return
}

func (c common) pdMarshal(dst *[]byte, sig signature, wrap []wrappedKey) error {
	// byteLen is copied from elliptic.go:Marshal()
	byteLen := (c.curve.Params().BitSize + 7) >> 3
	// n big enough for ciphersuite, sig.r, sig.s, len(wrap), {keyHash, encrypted, nonce, X, y}
	n := 1 + 2*byteLen + (1+5*len(wrap))*binary.MaxVarintLen64 +
		len(wrap)*(sha256.Size+(c.aesLen+gcmTagSize)+gcmStandardNonceSize+2*byteLen)
	// TODO great, but how is the ordinary user to know? maybe  PackdataLen(len(usernames))
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	// dst is now guaranteed large enough
	(*dst)[0] = byte(c.ciphersuite)
	n = 1
	n += pdPutBytes((*dst)[n:], sig.r.Bytes())
	n += pdPutBytes((*dst)[n:], sig.s.Bytes())
	n += binary.PutVarint((*dst)[n:], int64(len(wrap)))
	for _, w := range wrap {
		n += pdPutBytes((*dst)[n:], w.keyHash)
		n += pdPutBytes((*dst)[n:], w.encrypted)
		n += pdPutBytes((*dst)[n:], w.nonce)
		n += pdPutBytes((*dst)[n:], w.ephemeral.X.Bytes())
		n += pdPutBytes((*dst)[n:], w.ephemeral.Y.Bytes())
	}
	*dst = (*dst)[:n]
	return nil // err impossible for now but the night is young
}

func (c common) pdUnmarshal(pd []byte, name upspin.PathName) (sig signature, wrap []wrappedKey, err error) {
	if pd[0] != byte(c.ciphersuite) {
		return sig0, nil, fmt.Errorf("expected packing %d, got %d", c.ciphersuite, pd[0])
	}
	n := 1
	sig.r = big.NewInt(0)
	sig.s = big.NewInt(0)
	byteLen := (c.curve.Params().BitSize + 7) >> 3
	buf := make([]byte, byteLen)
	n += pdGetBytes(&buf, pd[n:])
	sig.r.SetBytes(buf)
	n += pdGetBytes(&buf, pd[n:])
	sig.s.SetBytes(buf)
	nwrap64, vlen := binary.Varint(pd[n:])
	n += vlen
	nwrap := int(nwrap64)
	if int64(nwrap) != nwrap64 {
		return sig0, nil, fmt.Errorf("implausible number of wrapped keys: %d\n", nwrap64)
	}
	wrap = make([]wrappedKey, nwrap)
	for i := 0; i < nwrap; i++ {
		var w wrappedKey
		w.keyHash = make([]byte, sha256.Size)
		w.encrypted = make([]byte, 100) // TODO len
		w.nonce = make([]byte, gcmStandardNonceSize)
		w.ephemeral = ecdsa.PublicKey{Curve: c.curve, X: big.NewInt(0), Y: big.NewInt(0)}
		n += pdGetBytes(&w.keyHash, pd[n:])
		n += pdGetBytes(&w.encrypted, pd[n:])
		n += pdGetBytes(&w.nonce, pd[n:])
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.X.SetBytes(buf)
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.Y.SetBytes(buf)
		wrap[i] = w
	}
	if n != len(pd) { // sanity check, not a thorough parser test
		return sig0, nil, fmt.Errorf("got %d, expected %d", n, len(pd))
	}
	return sig, wrap, nil
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
	if len(dkey) != c.aesLen {
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
	if len(dkey) != c.aesLen {
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

// parsePrivateKey returns an ECDSA private key given a user's ECDSA public key and a
// string representation of the private key.
func (c common) parsePrivateKey(publicKey *ecdsa.PublicKey, privateKey upspin.KeyPair) (priv *ecdsa.PrivateKey, err error) {
	if n := len(privateKey.Private) - 1; privateKey.Private[n] == '\n' {
		privateKey.Private = privateKey.Private[:n]
	}
	var d big.Int
	err = d.UnmarshalText([]byte(privateKey.Private))
	if err != nil {
		return nil, err
	}
	return &ecdsa.PrivateKey{PublicKey: *publicKey, D: &d}, nil
}

// publicKey returns the string representation of a user's public key.
func (c common) publicKey(ctx *upspin.Context, user upspin.UserName) (upspin.PublicKey, error) {
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
		if c.isValidKeyForPacker(k) {
			return k, nil
		}
	}
	return "", fmt.Errorf(noKnownKeysForUser, user)
}

// parsePublicKey takes a string representation of a
// public key and converts it into an ECDSA public key.
func (c common) parsePublicKey(publicKey upspin.PublicKey) (*ecdsa.PublicKey, error) {
	ecdsaPubKey, keyType, err := keyloader.ParsePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	if keyType != c.packerString {
		return nil, fmt.Errorf("expected packing %s, got %s", c.packerString, keyType)
	}
	return ecdsaPubKey, nil
}

func (c common) isValidKeyForPacker(publicKey upspin.PublicKey) bool {
	return strings.HasPrefix(string(publicKey), c.packerString)
}
