// Keygen creates local files secret.upspinkey and public.upspinkey in ~/.ssh
// which contain the private and public parts of a keypair.

// Eventually we'll offer something like ssh-agent, but we need
// to start with a usable and safe standalone tool.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"upspin.googlesource.com/upspin.git/pack/ee"
	"upspin.googlesource.com/upspin.git/pack/keygen/proquint"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	packing = flag.String("packing", "p256", "packing name, such as p256")
	seed    = flag.String("seed", "", "128 bit seed in proquint format")
	where   = flag.String("where", "", "directory to write keys. If empty, $HOME/.ssh/")
)

// drng is an io.Reader returning deterministic random bits seeded from aesKey.
type drng struct {
	aes     cipher.Block
	counter uint32
	random  []byte
}

func (d *drng) Read(p []byte) (n int, err error) {
	lenp := len(p)
	n = lenp
	for n > 0 {
		if len(d.random) == 0 {
			d.random = make([]byte, 16) // TODO try again to move this outside
			binary.BigEndian.PutUint32(d.random[0:4], d.counter)
			d.counter++
			binary.BigEndian.PutUint32(d.random[4:8], d.counter)
			d.counter++
			binary.BigEndian.PutUint32(d.random[8:12], d.counter)
			d.counter++
			binary.BigEndian.PutUint32(d.random[12:16], d.counter)
			d.counter++
			d.aes.Encrypt(d.random, d.random)
		}
		m := copy(p, d.random)
		n -= m
		p = p[m:]
		d.random = d.random[m:]
	}
	return lenp, nil
}

func createKeys(curve elliptic.Curve, packer upspin.Packer) {

	// Pick seed with 128 bits of entropy.
	b := make([]byte, 16)
	if len(*seed) > 0 {
		if len((*seed)) != 47 || (*seed)[5] != '-' || (*seed)[23] != ' ' {
			log.Fatalf("expected seed like\n lusab-babad-gutih-tugad gutuk-bisog-mudof-sakat\n"+
				"not\n %s\nkey not generated", *seed)
		}
		for i := 0; i < 8; i++ {
			binary.BigEndian.PutUint16(b[2*i:2*i+2], proquint.Decode([]byte((*seed)[6*i:6*i+5])))
		}
	} else {
		_, err := rand.Read(b)
		if err != nil {
			log.Fatalf("key not generated: %s", err)
		}
		proquints := make([]string, 8)
		for i := 0; i < 8; i++ {
			proquints[i] = string(proquint.Encode(binary.BigEndian.Uint16(b[2*i : 2*i+2])))
		}
		// fmt.Printf("-seed \"%s-%s-%s-%s %s-%s-%s-%s\"\n", proquints...)
		fmt.Printf("-seed \"%s-%s-%s-%s %s-%s-%s-%s\"\n", proquints[0], proquints[1], proquints[2],
			proquints[3], proquints[4], proquints[5], proquints[6], proquints[7])
	}

	// Create crypto deterministic random generator from b.
	d := &drng{}
	cipher, err := aes.NewCipher(b)
	if err != nil {
		panic("can't happen")
	}
	d.aes = cipher

	// Generate random key-pair.
	priv, err := ecdsa.GenerateKey(curve, d)
	if err != nil {
		log.Fatalf("key not generated: %s", err)
	}

	// Save the keys to files.
	private, err := os.Create(filepath.Join(keydir(), "secret.upspinkey"))
	if err != nil {
		log.Fatal(err)
	}
	err = private.Chmod(0600)
	if err != nil {
		log.Fatal(err)
	}
	public, err := os.Create(filepath.Join(keydir(), "public.upspinkey"))
	if err != nil {
		log.Fatal(err)
	}
	_, err = private.WriteString(priv.D.String() + "\n")
	if err != nil {
		log.Fatal(err)
	}
	_, err = public.WriteString(packer.String() + "\n" + priv.X.String() + "\n" + priv.Y.String() + "\n")
	if err != nil {
		log.Fatal(err)
	}
	err = private.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = public.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	// because ee.common.curve is not exported
	curve := []elliptic.Curve{16: elliptic.P256(), 18: elliptic.P384(), 17: elliptic.P521()}

	log.SetFlags(0)
	log.SetPrefix("keygen: ")
	flag.Parse()

	p, ok := ee.Packer[*packing]
	if !ok {
		log.Fatal("unrecognized packing")
	}
	i := p.Packing()
	createKeys(curve[i], p)
}

func keydir() string {
	if where != nil && len(*where) > 0 {
		return *where
	}
	user, err := user.Current()
	if err != nil {
		log.Fatal("no user")
	}
	return filepath.Join(user.HomeDir, ".ssh")
}
