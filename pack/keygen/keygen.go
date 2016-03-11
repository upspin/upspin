// Keygen creates local files secret.upspinkey and public.upspinkey in ~/.ssh
// which contain the private and public parts of a keypair.
// Eventually this will be provided by ssh-agent or e2email
// or something else, but we need a minimally usable and
// safe tool for initial testing.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"flag"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"upspin.googlesource.com/upspin.git/pack/ee"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	packing = flag.String("packing", "p256", "packing name, such as p256")
	where   = flag.String("where", "", "directory where to write keys. If empty, put in $HOME/.ssh/")
)

func createKeys(curve elliptic.Curve, packer upspin.Packer) {
	// TODO get 128bit seed from rand.Random, print proquints, create random generator from that seed
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		log.Fatalf("key not generated: %s", err)
	}

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
