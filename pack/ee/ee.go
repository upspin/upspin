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

	"golang.org/x/crypto/hkdf"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

type eep256 struct{}
type eep521 struct{}

var _ upspin.Packer = eep256{}
var _ upspin.Packer = eep521{}

func init() {
	pack.Register(eep256{})
	pack.Register(eep521{})
}

var (
	errTooShort = errors.New("EEPack: destination slice too short")
	errMetaNil  = errors.New("EEPack: nil Metadata")
	sig0        signature // for returning nil of correct type
	ciphersuite upspin.Packing
	curve       elliptic.Curve
)

type signature struct {
	r *big.Int
	s *big.Int
}

// wrappedKey holds a key that will decrypt file contents.
type wrappedKey struct {
	keyhash   []byte // sha256(PublicKey)
	encrypted []byte // Data decryption key, itself encrypted using public key of user.
	nonce     []byte
	eV        ecdsa.PublicKey // ephemeral public key      TODO consider renaming
}
type wrappedKeys []wrappedKey

func (e eep256) Packing() upspin.Packing {
	return upspin.EEp256Pack
}

func (e eep521) Packing() upspin.Packing {
	return upspin.EEp521Pack
}

// aeswrap implements our version of RFC6637 §8 or NIST 800-56Ar2
func aeswrap(R *ecdsa.PublicKey, own *ecdsa.PrivateKey, dkey []byte) (w wrappedKey, err error) {
	// Step 1.  Create shared Diffie-Hellman secret.
	// v, V=vG  ephemeral keypair
	// S = vR   shared point
	v, err := ecdsa.GenerateKey(curve, rand.Reader)
	sx, sy := curve.ScalarMult(R.X, R.Y, v.D.Bytes())
	S := elliptic.Marshal(curve, sx, sy)

	// Step 2.  Convert secret to HKDF strong secret.
	w.nonce = make([]byte, 12) // TODO 12 should be aead.NonceSize() but we don't have aead yet
	_, err = rand.Read(w.nonce)
	if err != nil {
		return
	}
	mess := []byte(R.X.String() + " " + R.Y.String()) // TODO turn into a func
	keyhash := sha256.Sum256(mess)
	w.keyhash = keyhash[:]
	w.encrypted = make([]byte, len(mess)+16) // TODO 16 should be aead.Overhead()

	mess = []byte(fmt.Sprintf("%2x:%x:%x", ciphersuite, w.keyhash, w.nonce))
	hash := sha256.New
	hkdf := hkdf.New(hash, S, nil, mess) // TODO reconsider salt
	strong := make([]byte, 16)           // TODO length depends on ciphersuite
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
	aead.Seal(w.encrypted, w.nonce, dkey, nil)
	// see also https://github.com/golang/crypto/blob/master/openpgp/packet/encrypted_key.go
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
	strong := make([]byte, 16) // TODO length depends on ciphersuite
	_, err = io.ReadFull(hkdf, strong)
	if err != nil {
		return
	}
	block, err := aes.NewCipher(strong)
	if err != nil {
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return
	}

	// Step 3. Encrypt dkey.
	dkey = make([]byte, 16)
	dkey, err = aead.Open(dkey, w.nonce, w.encrypted, nil)
	return
}

func (e eep256) Pack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp256Pack
	curve = elliptic.P256()
	return eepack(ciphertext, cleartext, meta, name)
}

func (e eep521) Pack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp521Pack
	curve = elliptic.P521()
	return eepack(ciphertext, cleartext, meta, name)
}

func eepack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	if meta == nil {
		return 0, errMetaNil
	}
	dkey := make([]byte, 16)
	_, err := rand.Read(dkey)
	if err != nil {
		return 0, err
	}
	n, err := encrypt(ciphertext, cleartext, dkey)
	if err != nil {
		return 0, err
	}

	parsed, err := path.Parse(name)
	if err != nil {
		return 0, fmt.Errorf("EEPack: %v", err)
	}
	owner := string(parsed.User)
	usernames := []string{owner} // TODO should be readers of directory

	mess := []byte(fmt.Sprintf("%2x:%s:%x:%q", ciphersuite, name, dkey, ciphertext))
	privkey, err := privatekey(owner)
	if err != nil {
		return 0, nil
	}
	messhash := sha256.Sum256(mess)
	r, s, err := ecdsa.Sign(rand.Reader, privkey, messhash[:])
	if err != nil {
		return 0, fmt.Errorf("EEPack: ECDSA failed, %v", err)
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
	return n, err
}

func (e eep256) Unpack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp256Pack
	curve = elliptic.P256()
	return unpack(cleartext, ciphertext, meta, name)
}

func (e eep521) Unpack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	ciphersuite = upspin.EEp521Pack
	curve = elliptic.P521()
	return unpack(cleartext, ciphertext, meta, name)
}

func unpack(cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	if meta == nil {
		return 0, errMetaNil
	}
	var errMismatch error
	dkey := make([]byte, 16)
	sig, wrap, err := pdUnmarshal(meta.PackData, name)
	if err != nil {
		return 0, fmt.Errorf("EEPack: %v", err)
	}
	parsed, err := path.Parse(name)
	owner := string(parsed.User) // TODO do we get this from Client context now?
	if err != nil {
		return 0, fmt.Errorf("EEPack: %v", err)
	}
	recipient := owner
	privkey, err := privatekey(recipient)
	if err != nil {
		return 0, err
	}
	pubkey := privkey.PublicKey // of recipient
	mess := []byte(pubkey.X.String() + " " + pubkey.Y.String())
	keyhash := sha256.Sum256(mess)
	rhash := keyhash[:]
	for _, w := range wrap {
		if bytes.Equal(rhash, w.keyhash) {
			continue
		}
		dkey, err = aesunwrap(privkey, w)
		if err != nil {
			continue
		}
		mess := []byte(fmt.Sprintf("%2x:%s:%x:%x", ciphersuite, name, dkey, ciphertext))
		sum := sha256.Sum256(mess)
		if !ecdsa.Verify(&pubkey, sum[:], sig.r, sig.s) {
			errMismatch = fmt.Errorf("does not verify")
			continue // maybe one of the other keys will work
		}
		return decrypt(cleartext, ciphertext, dkey)
	}
	if errMismatch != nil {
		return 0, errMismatch
	}
	return 0, fmt.Errorf("no wrapped key for me")
}

func (eep256) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (eep521) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (eep256) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (eep521) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (eep256) String() string {
	return "ee"
}

func (eep521) String() string {
	return "ee"
}

func pdMarshal(sig signature, wrap []wrappedKey) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, sig)
	if err != nil {
		return nil, err
	}
	// Encode the number of wrapped keys.
	err = binary.Write(buf, binary.BigEndian, uint64(len(wrap)))
	if err != nil {
		return nil, err
	}
	for _, w := range wrap {
		err = binary.Write(buf, binary.BigEndian, []byte(w.keyhash))
		if err != nil {
			return nil, err
		}
		err = binary.Write(buf, binary.BigEndian, w.encrypted)
		if err != nil {
			return nil, err
		}
		err = binary.Write(buf, binary.BigEndian, w.nonce)
		if err != nil {
			return nil, err
		}
		err = binary.Write(buf, binary.BigEndian, w.eV)
		if err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), err
}

func pdUnmarshal(pd []byte, name upspin.PathName) (sig signature, wrap []wrappedKey, err error) {
	buf := bytes.NewReader(pd)
	err = binary.Read(buf, binary.BigEndian, sig)
	if err != nil {
		return sig0, nil, err
	}
	n := uint64(0)
	err = binary.Read(buf, binary.BigEndian, &n)
	if err != nil {
		return sig0, nil, err
	}
	wrap = make([]wrappedKey, n)
	for i := 0; i < int(n); i++ {
		err = binary.Read(buf, binary.BigEndian, wrap[i])
		if err != nil {
			return sig0, nil, err
		}
	}
	return sig, wrap, err
}

func encrypt(ciphertext, cleartext, dkey []byte) (int, error) {
	if len(dkey) != 16 {
		// should recognize 32 as AES 256
		return 0, errors.New("unimplemented key type")
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
	if len(dkey) != 16 {
		return 0, errors.New("unimplemented key type")
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
	f, err := os.Open("secret.upspinkey")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 200) // big enough for P-521
	n, err := f.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("privatekey read: %v", err)
	}
	d := big.NewInt(0)
	err = d.UnmarshalText(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("privatekey parse: %v", err)
	}
	return &ecdsa.PrivateKey{*pubkey, d}, nil
}

func publickey(user string) (priv *ecdsa.PublicKey, err error) {
	// TODO replace someday by keyserver
	f, err := os.Open("public.upspinkey")
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
