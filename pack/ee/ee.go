// Package ee implements elliptic-curve end-to-end-encrypted packers EEp256Pack and EEp521Pack.
// crypto summary:
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
	"math/big"
	"os"
	"os/user"
	"path/filepath"

	"golang.org/x/crypto/hkdf"
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

type eep256 struct{}
type eep521 struct{}

var _ upspin.Packer = eep256{}
var _ upspin.Packer = eep521{}

func init() {
	pack.Register(eep256{})
	pack.Register(eep521{})
}

const (
	// TODO unfortunately cipher/gcm.go doesn't export these
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

var (
	errTooShort     = errors.New("destination slice too short")
	errMetaNil      = errors.New("nil Metadata")
	errVerify       = errors.New("does not verify")
	errNoWrappedKey = errors.New("no wrapped key for me")
	errKeyLength    = errors.New("wrong key length")
	sig0            signature // for returning nil of correct type
)

var (
	// These Packer-specific values are set by Pack and Unpack.
	// TODO There is no locking, so this seems unsafe, but it will do for the moment.
	ciphersuite upspin.Packing
	aesLen      int
	curve       elliptic.Curve
)

func (e eep256) Packing() upspin.Packing {
	return upspin.EEp256Pack
}

func (e eep521) Packing() upspin.Packing {
	return upspin.EEp521Pack
}

func (e eep256) PackLen(ctx *upspin.Context, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	if err := pack.CheckPackMeta(e, meta); err != nil {
		return -1
	}
	return len(cleartext)
}

func (e eep521) PackLen(ctx *upspin.Context, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	if err := pack.CheckPackMeta(e, meta); err != nil {
		return -1
	}
	return len(cleartext)
}

func (e eep256) UnpackLen(ctx *upspin.Context, ciphertext []byte, meta *upspin.Metadata) int {
	if err := pack.CheckUnpackMeta(e, meta); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (e eep521) UnpackLen(ctx *upspin.Context, ciphertext []byte, meta *upspin.Metadata) int {
	if err := pack.CheckUnpackMeta(e, meta); err != nil {
		return -1
	}
	return len(ciphertext)
}

func (eep256) String() string {
	return "eep256"
}

func (eep521) String() string {
	return "eep521"
}

func (e eep256) Pack(ctx *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckPackMeta(e, meta); err != nil {
		return 0, err
	}
	ciphersuite = upspin.EEp256Pack
	curve = elliptic.P256()
	aesLen = 16
	return eePack(ciphertext, cleartext, meta, name)
}

func (e eep521) Pack(ctx *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckPackMeta(e, meta); err != nil {
		return 0, err
	}
	ciphersuite = upspin.EEp521Pack
	curve = elliptic.P521()
	aesLen = 32
	return eePack(ciphertext, cleartext, meta, name)
}

func (e eep256) Unpack(ctx *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckUnpackMeta(e, meta); err != nil {
		return 0, err
	}
	ciphersuite = upspin.EEp256Pack
	curve = elliptic.P256()
	aesLen = 16
	return eeUnpack(cleartext, ciphertext, meta, name)
}

func (e eep521) Unpack(ctx *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckUnpackMeta(e, meta); err != nil {
		return 0, err
	}
	ciphersuite = upspin.EEp521Pack
	curve = elliptic.P521()
	aesLen = 32
	return eeUnpack(cleartext, ciphertext, meta, name)
}

func eePack(ciphertext, cleartext []byte, meta *upspin.Metadata, pathname upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	ciphertext = ciphertext[:len(cleartext)]
	if meta == nil {
		return 0, errMetaNil
	}
	dkey := make([]byte, aesLen)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, err
	}
	nCipher, err := encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, err
	}

	// TODO  switch to Context.UserName, .Packing, and .PrivateKey when test harness is in place
	parsed, err := path.Parse(pathname)
	if err != nil {
		return 0, err
	}
	owner := string(parsed.User)
	usernames := []string{owner} // TODO should be readers of directory
	privateKey, err := privateKey(owner)
	if err != nil {
		return 0, err
	}

	r, s, err := ecdsa.Sign(rand.Reader, privateKey, verHash(pathname, dkey, ciphertext))
	if err != nil {
		return 0, err
	}
	sig := signature{r, s}
	wrap := make([]wrappedKey, len(usernames))
	for i, _ := range usernames {
		wrap[i], err = aesWrap(&privateKey.PublicKey, privateKey, dkey)
		if err != nil {
			return 0, err
		}
	}
	err = pdMarshal(&meta.PackData, sig, wrap)
	return nCipher, err
}

func eeUnpack(cleartext, ciphertext []byte, meta *upspin.Metadata, pathname upspin.PathName) (int, error) {
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	cleartext = cleartext[:len(ciphertext)]
	if meta == nil {
		return 0, errMetaNil
	}
	dkey := make([]byte, aesLen)
	sig, wrap, err := pdUnmarshal(meta.PackData, pathname)
	if err != nil {
		return 0, err
	}

	// TODO get from Context
	parsed, err := path.Parse(pathname)
	owner := string(parsed.User)
	if err != nil {
		return 0, err
	}
	recipient := owner
	privateKey, err := privateKey(recipient)
	if err != nil {
		return 0, err
	}
	pubkey := privateKey.PublicKey // of recipient
	rhash := keyHash(&pubkey)
	for _, w := range wrap {
		if !bytes.Equal(rhash, w.keyHash) {
			fmt.Printf("unequal %x\n        %x\n", rhash, w.keyHash)
			continue
		}
		dkey, err = aesUnwrap(privateKey, w)
		if err != nil {
			return 0, err
		}
		if !ecdsa.Verify(&pubkey, verHash(pathname, dkey, ciphertext), sig.r, sig.s) {
			return 0, errVerify
		}
		return decrypt(cleartext, ciphertext, dkey)
	}
	return 0, errNoWrappedKey
}

func verHash(pathname upspin.PathName, dkey, ciphertext []byte) []byte {
	// TODO Consider alternative crypto that merges verification with wrapping.
	// TODO If we stick with Sign, consider streaming ciphertext to sha256 here.
	mess := []byte(fmt.Sprintf("%02x:%s:%x:%x", ciphersuite, pathname, dkey, ciphertext))
	messhash := sha256.Sum256(mess)
	return messhash[:]
}

func keyHash(p *ecdsa.PublicKey) []byte {
	keybytes := []byte(p.X.String() + ":upspinkeyHash:" + p.Y.String())
	keyHash := sha256.Sum256(keybytes)
	return keyHash[:]
}

// aesWrap implements NIST 800-56Ar2; see also RFC6637 §8.
func aesWrap(R *ecdsa.PublicKey, own *ecdsa.PrivateKey, dkey []byte) (w wrappedKey, err error) {

	// Step 1.  Create shared Diffie-Hellman secret.
	// v, V=vG  ephemeral key pair
	// S = vR   shared point
	v, err := ecdsa.GenerateKey(curve, rand.Reader)
	sx, sy := curve.ScalarMult(R.X, R.Y, v.D.Bytes())
	S := elliptic.Marshal(curve, sx, sy)
	w.ephemeral = ecdsa.PublicKey{Curve: curve, X: v.X, Y: v.Y}

	// Step 2.  Convert shared secret to strong secret via HKDF.
	w.nonce = make([]byte, gcmStandardNonceSize)
	_, err = rand.Read(w.nonce)
	if err != nil {
		return
	}
	w.keyHash = keyHash(R)
	mess := []byte(fmt.Sprintf("%02x:%x:%x", ciphersuite, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess) // TODO reconsider salt
	strong := make([]byte, aesLen)
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

func aesUnwrap(R *ecdsa.PrivateKey, w wrappedKey) (dkey []byte, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// S = rV
	sx, sy := curve.ScalarMult(w.ephemeral.X, w.ephemeral.Y, R.D.Bytes())
	S := elliptic.Marshal(curve, sx, sy)

	// Step 2.  Convert shared secret to strong secret via HKDF.
	mess := []byte(fmt.Sprintf("%02x:%x:%x", ciphersuite, w.keyHash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess)
	strong := make([]byte, aesLen)
	_, err = io.ReadFull(hkdf, strong)
	if err != nil {
		return
	}

	// Step 3. Decrypt dkey.
	block, err := aes.NewCipher(strong)
	if err != nil {
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return
	}
	dkey = make([]byte, 0, aesLen)
	dkey, err = aead.Open(dkey, w.nonce, w.encrypted, nil)
	return
}

func pdMarshal(dst *[]byte, sig signature, wrap []wrappedKey) error {
	// byteLen is copied from elliptic.go:Marshal()
	byteLen := (curve.Params().BitSize + 7) >> 3
	// n big enough for ciphersuite, sig.r, sig.s, len(wrap), {keyHash, encrypted, nonce, X, y}
	n := 1 + 2*byteLen + (1+5*len(wrap))*binary.MaxVarintLen64 +
		len(wrap)*(sha256.Size+(aesLen+gcmTagSize)+gcmStandardNonceSize+2*byteLen)
	// TODO great, but how is the ordinary user to know? maybe  PackdataLen(len(usernames))
	if len(*dst) < n {
		*dst = make([]byte, n)
	}
	// dst is now guaranteed large enough
	(*dst)[0] = byte(ciphersuite)
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

func pdUnmarshal(pd []byte, name upspin.PathName) (sig signature, wrap []wrappedKey, err error) {
	if pd[0] != byte(ciphersuite) {
		return sig0, nil, fmt.Errorf("expected packing %d, got %d", ciphersuite, pd[0])
	}
	n := 1
	sig.r = big.NewInt(0)
	sig.s = big.NewInt(0)
	byteLen := (curve.Params().BitSize + 7) >> 3
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
		w.ephemeral = ecdsa.PublicKey{Curve: curve, X: big.NewInt(0), Y: big.NewInt(0)}
		n += pdGetBytes(&w.keyHash, pd[n:])
		n += pdGetBytes(&w.encrypted, pd[n:])
		n += pdGetBytes(&w.nonce, pd[n:])
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.X.SetBytes(buf)
		n += pdGetBytes(&buf, pd[n:])
		w.ephemeral.Y.SetBytes(buf)
		wrap[i] = w
		if n != len(pd) { // sanity check, not a thorough parser test
			return sig0, nil, fmt.Errorf("got %d, expected %d", n, len(pd))
		}
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

func encrypt(ciphertext, cleartext, dkey []byte) (int, error) {
	if len(dkey) != aesLen {
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

func decrypt(cleartext, ciphertext, dkey []byte) (int, error) {
	if len(dkey) != aesLen {
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

// privateKey returns the private key of user by reading file from ~/.ssh/.
func privateKey(user string) (priv *ecdsa.PrivateKey, err error) {
	// TODO move to code that sets Context?
	// TODO replace someday by a safe variant of ssh-agent
	pubkey, err := publicKey(user)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(sshdir(), fmt.Sprintf("secret.%d.upspinkey", ciphersuite)))
	if err != nil {
		fmt.Printf("If you haven't already, run keygen.\n")
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 200) // big enough for P-521
	n, err := f.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("privateKey read: %v", err)
	}
	if buf[n-1] == '\n' {
		n--
	}
	var d big.Int
	err = d.UnmarshalText(buf[:n])
	if err != nil {
		return nil, err
	}
	return &ecdsa.PrivateKey{PubicKey: *pubkey, D: &d}, nil
}

// publicKey returns the public key of user by reading file from ~/.ssh/.
func publicKey(user string) (priv *ecdsa.PublicKey, err error) {
	// TODO replace someday by keyserver
	f, err := os.Open(filepath.Join(sshdir(), fmt.Sprintf("public.%d.upspinkey", ciphersuite)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var x, y big.Int
	n, err := fmt.Fscan(io.Reader(f), &x, &y)
	if err != nil || n != 2 {
		return nil, err
	}
	return &ecdsa.PublicKey{Curve: curve, X: &x, Y: &y}, nil
}

func sshdir() string {
	user, err := user.Current()
	if err != nil {
		panic("no user")
	}
	return filepath.Join(user.HomeDir, ".ssh")
}
