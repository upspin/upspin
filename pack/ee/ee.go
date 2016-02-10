// Package ee implements elliptic-curve end-to-end-encrypted packers EEp256Pack and EEp521Pack.
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

// wrappedKey encodes a key that will decrypt the contents.
type wrappedKey struct {
	keyhash   []byte // sha256(PublicKey)
	encrypted []byte // Data decryption key, itself encrypted using public key of user.
	nonce     []byte
	eV        ecdsa.PublicKey // ephemeral public key      TODO consider renaming
}
type wrappedKeys []wrappedKey

// for testing purposes, encrypt on Store but not in Directory
var backdoor []byte // TODO remove before flight

type eep256 struct{}
type eep521 struct{}

var _ upspin.Packer = eep256{}
var _ upspin.Packer = eep521{}

func init() {
	pack.Register(eep256{})
	pack.Register(eep521{})
}

const (
	// unfortunately cipher/gcm.go doesn't export these
	gcmStandardNonceSize = 12
	gcmTagSize           = 16
)

var (
	errTooShort = errors.New("EEPack: destination slice too short")
	errMetaNil  = errors.New("EEPack: nil Metadata")
	sig0        signature // for returning nil of correct type
)

var (
	// These Packer-specific values are set by Pack and Unpack.
	// There is no locking, so this seems unsafe, but will do for the moment as we test.
	ciphersuite upspin.Packing
	aeslen      int
	curve       elliptic.Curve
)

func (e eep256) Packing() upspin.Packing {
	return upspin.EEp256Pack
}

func (e eep521) Packing() upspin.Packing {
	return upspin.EEp521Pack
}

func (eep256) PackLen(ctx *upspin.ClientContext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (eep521) PackLen(ctx *upspin.ClientContext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (eep256) UnpackLen(ctx *upspin.ClientContext, ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (eep521) UnpackLen(ctx *upspin.ClientContext, ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (eep256) String() string {
	return "eep256"
}

func (eep521) String() string {
	return "eep521"
}

func (e eep256) Pack(ctx *upspin.ClientContext, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp256Pack
	curve = elliptic.P256()
	aeslen = 16
	return eepack(ciphertext, cleartext, meta, name)
}

func (e eep521) Pack(ctx *upspin.ClientContext, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp521Pack
	curve = elliptic.P521()
	aeslen = 32
	return eepack(ciphertext, cleartext, meta, name)
}

func (e eep256) Unpack(ctx *upspin.ClientContext, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp256Pack
	curve = elliptic.P256()
	aeslen = 16
	return eeunpack(cleartext, ciphertext, meta, name)
}

func (e eep521) Unpack(ctx *upspin.ClientContext, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp521Pack
	curve = elliptic.P521()
	aeslen = 32
	return eeunpack(cleartext, ciphertext, meta, name)
}

func eepack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	ciphertext = ciphertext[:len(cleartext)]
	if meta == nil {
		return 0, errMetaNil
	}
	dkey := make([]byte, aeslen)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, err
	}
	backdoor = make([]byte, aeslen)
	copy(backdoor, dkey) // TODO remove before flight
	ncipher, err := encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, err
	}

	// TODO  get owner and privkey from Context
	parsed, err := path.Parse(name)
	if err != nil {
		return 0, fmt.Errorf("eepack: %v", err)
	}
	owner := string(parsed.User)
	usernames := []string{owner} // TODO should be readers of directory
	privkey, err := privatekey(owner)
	if err != nil {
		return 0, err
	}

	mess := []byte(fmt.Sprintf("%2x:%s:%x:%q", ciphersuite, name, dkey, ciphertext))
	messhash := sha256.Sum256(mess)
	r, s, err := ecdsa.Sign(rand.Reader, privkey, messhash[:])
	if err != nil {
		return 0, err
	}
	sig := signature{r, s}
	wrap := make([]wrappedKey, len(usernames))
	for i, _ := range usernames {
		wrap[i], err = aeswrap(&privkey.PublicKey, privkey, dkey)
		if err != nil {
			return 0, err
		}
	}
	meta.PackData, err = pdMarshal(sig, wrap)
	return ncipher, err
}

func eeunpack(cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	cleartext = cleartext[:len(ciphertext)]
	if meta == nil {
		return 0, errMetaNil
	}
	dkey := make([]byte, aeslen)
	sig, wrap, err := pdUnmarshal(meta.PackData, name)
	if err != nil {
		return 0, fmt.Errorf("EEPack: %v", err)
	}

	// TODO get from Context
	parsed, err := path.Parse(name)
	owner := string(parsed.User)
	if err != nil {
		return 0, fmt.Errorf("EEPack: %v", err)
	}
	recipient := owner
	privkey, err := privatekey(recipient)
	if err != nil {
		return 0, err
	}
	pubkey := privkey.PublicKey // of recipient

	keybytes := []byte(pubkey.X.String() + " " + pubkey.Y.String())
	keyhash := sha256.Sum256(keybytes)
	rhash := keyhash[:]
	fmt.Printf("searching for key %x\n", rhash)
	var errMismatch error
	for _, w := range wrap {
		fmt.Printf("      try keyhash %x\n", w.keyhash)
		if !bytes.Equal(rhash, w.keyhash) {
			continue
		}
		dkey, err = aesunwrap(privkey, w)
		if err != nil {
			continue
		}
		mess := []byte(fmt.Sprintf("%2x:%s:%x:%x", ciphersuite, name, dkey, ciphertext))
		sum := sha256.Sum256(mess)
		fmt.Printf("%s %x\n", mess, sum)
		if !ecdsa.Verify(&pubkey, sum[:], sig.r, sig.s) {
			errMismatch = fmt.Errorf("does not verify")
			fmt.Printf("%s\n", errMismatch)
			continue // maybe one of the other keys will work
		}
		if !bytes.Equal(dkey, backdoor) { // TODO remove before flight
			fmt.Printf("got the wrong decryption key\n")
		}
		return decrypt(cleartext, ciphertext, backdoor)
	}
	if errMismatch != nil {
		fmt.Printf("no wrapped key, proceed anyway\n") // TODO remove before flight
		return decrypt(cleartext, ciphertext, backdoor)
		// return 0, errMismatch
	}
	fmt.Printf("no wrapped key for me, proceeding anyway via backdoor\n")
	return decrypt(cleartext, ciphertext, backdoor) // TODO remove before flight
	// return 0, fmt.Errorf("no wrapped key for me")
}

// aeswrap implements our version of RFC6637 §8 or NIST 800-56Ar2
func aeswrap(R *ecdsa.PublicKey, own *ecdsa.PrivateKey, dkey []byte) (w wrappedKey, err error) {

	// Step 1.  Create shared Diffie-Hellman secret.
	// v, V=vG  ephemeral keypair
	// S = vR   shared point
	v, err := ecdsa.GenerateKey(curve, rand.Reader)
	sx, sy := curve.ScalarMult(R.X, R.Y, v.D.Bytes())
	S := elliptic.Marshal(curve, sx, sy)
	w.eV = ecdsa.PublicKey{curve, v.X, v.Y}

	// Step 2.  Convert secret to HKDF strong secret.
	w.nonce = make([]byte, gcmStandardNonceSize)
	_, err = rand.Read(w.nonce)
	if err != nil {
		return
	}
	keybytes := []byte(R.X.String() + " " + R.Y.String()) // TODO turn into a func
	keyhash := sha256.Sum256(keybytes)
	w.keyhash = keyhash[:]

	mess := []byte(fmt.Sprintf("%2x:%x:%x", ciphersuite, w.keyhash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess) // TODO reconsider salt
	strong := make([]byte, aeslen)
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
	fmt.Printf("dkey     %x\n", dkey)
	w.encrypted = make([]byte, 0, len(dkey)+gcmTagSize)
	w.encrypted = aead.Seal(w.encrypted, w.nonce, dkey, nil)
	// TODO figure out why aead.Seal allocated memory here
	return
}

func aesunwrap(R *ecdsa.PrivateKey, w wrappedKey) (dkey []byte, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// S = rV
	sx, sy := curve.ScalarMult(w.eV.X, w.eV.Y, R.D.Bytes())
	S := elliptic.Marshal(curve, sx, sy)

	// Step 2.  Convert secret to HKDF strong secret.
	mess := []byte(fmt.Sprintf("%2x:%x:%x", ciphersuite, w.keyhash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess)
	strong := make([]byte, aeslen)
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
	dkey = make([]byte, 0, aeslen)
	dkey, err = aead.Open(dkey, w.nonce, w.encrypted, nil)
	return
}

// pdPutBytes puts length header in dst and then copies src to dst; returns bytes consumed
func pdPutBytes(dst, src []byte) int {
	vlen := binary.PutVarint(dst, int64(len(src)))
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
		panic("varint")
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

func pdMarshal(sig signature, wrap []wrappedKey) ([]byte, error) {
	// dst is guaranteed to be large enough;    assume Varint could be 10
	dst := make([]byte, 200+len(wrap)*(sha256.Size+128+2*aeslen+116+10000)) // TODO len
	n := 0
	n += pdPutBytes(dst[n:], backdoor) // TODO remove before flight
	n += pdPutBytes(dst[n:], sig.r.Bytes())
	n += pdPutBytes(dst[n:], sig.s.Bytes())
	n += binary.PutVarint(dst[n:], int64(len(wrap)))
	for i, w := range wrap {
		fmt.Printf("wrap[%d]\n  keyhash %v\n  encrypted %v\n  nonce %v\n  V %v\n", i, w.keyhash, w.encrypted, w.nonce, w.eV)
		n += pdPutBytes(dst[n:], w.keyhash)
		n += pdPutBytes(dst[n:], w.encrypted)
		n += pdPutBytes(dst[n:], w.nonce)
		if w.eV.X == nil { // TODO remove before flight
			w.eV.X = big.NewInt(0)
			w.eV.Y = big.NewInt(0)
		}
		n += pdPutBytes(dst[n:], w.eV.X.Bytes())
		n += pdPutBytes(dst[n:], w.eV.Y.Bytes())
	}
	dst = dst[:n]
	return dst, nil // no err possible for now but the night is young
}

func pdUnmarshal(pd []byte, name upspin.PathName) (sig signature, wrap []wrappedKey, err error) {
	sig.r = big.NewInt(0)
	sig.s = big.NewInt(0)
	backdoor = make([]byte, aeslen)
	buf := make([]byte, 2000)
	n := 0
	n += pdGetBytes(&backdoor, pd[n:]) // TODO remove before flight
	n += pdGetBytes(&buf, pd[n:])
	sig.r.SetBytes(buf)
	n += pdGetBytes(&buf, pd[n:])
	sig.s.SetBytes(buf)
	nwrap64, vlen := binary.Varint(pd[n:])
	n += vlen
	nwrap := int(nwrap64)
	wrap = make([]wrappedKey, nwrap)
	for i := 0; i < nwrap; i++ {
		var w wrappedKey
		w.keyhash = make([]byte, sha256.Size)
		w.encrypted = make([]byte, 100) // TODO len
		w.nonce = make([]byte, gcmStandardNonceSize)
		w.eV = ecdsa.PublicKey{curve, big.NewInt(0), big.NewInt(0)}
		n += pdGetBytes(&(w.keyhash), pd[n:])
		n += pdGetBytes(&(w.encrypted), pd[n:])
		n += pdGetBytes(&(w.nonce), pd[n:])
		n += pdGetBytes(&buf, pd[n:])
		w.eV.X.SetBytes(buf)
		n += pdGetBytes(&buf, pd[n:])
		w.eV.Y.SetBytes(buf)
		wrap[i] = w
		if n != len(pd) { // sanity check, not a thorough parser test
			return sig0, nil, fmt.Errorf("got %d, expected %d", n, len(pd))
		}
	}
	return sig, wrap, nil
}

func encrypt(ciphertext, cleartext, dkey []byte) (int, error) {
	if len(dkey) != aeslen {
		return 0, errors.New("wrong key length")
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
	if len(dkey) != aeslen {
		return 0, errors.New("wrong key len")
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

func privatekey(user string) (priv *ecdsa.PrivateKey, err error) {
	// TODO replace someday by a safe variant of ssh-agent
	pubkey, err := publickey(user)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(sshdir() + "secret.upspinkey")
	if err != nil {
		fmt.Printf("If you haven't already, run ../keygen/keygen.go.\n")
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 200) // big enough for P-521
	n, err := f.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("privatekey read: %v", err)
	}
	d := big.NewInt(0)
	if buf[n-1] == '\n' {
		n-- // TODO  There's gotta be a better way.
	}
	err = d.UnmarshalText(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("privatekey parse: %v", err)
	}
	return &ecdsa.PrivateKey{*pubkey, d}, nil
}

func publickey(user string) (priv *ecdsa.PublicKey, err error) {
	// TODO replace someday by keyserver
	f, err := os.Open(sshdir() + "public.upspinkey")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	x := big.NewInt(0)
	y := big.NewInt(0)
	n, err := fmt.Fscan(io.Reader(f), x, y)
	if err != nil || n != 2 {
		return nil, fmt.Errorf("publickey read: %v", err)
	}
	return &ecdsa.PublicKey{curve, x, y}, nil
}

func sshdir() string {
	user, err := user.Current()
	if err != nil {
		return "" // hence caller will use current working directory
	}
	return user.HomeDir + "/.ssh/"
}
