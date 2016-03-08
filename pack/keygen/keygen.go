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

func createKeys(curve elliptic.Curve, packing upspin.Packing) {
	// TODO get 128bit seed from rand.Random, print proquints, create random generator from that seed
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		log.Fatalf("key not generated: %s", err)
	}

	private, err := os.Create(filepath.Join(sshdir(), "secret.upspinkey"))
	if err != nil {
		log.Fatal(err)
	}
	err = private.Chmod(0600)
	if err != nil {
		log.Fatal(err)
	}

	public, err := os.Create(filepath.Join(sshdir(), "public.upspinkey"))
	if err != nil {
		log.Fatal(err)
	}
	_, err = private.WriteString(priv.D.String() + "\n")
	if err != nil {
		log.Fatal(err)
	}
	_, err = public.WriteString(ee.PackName[packing] + "\n" + priv.X.String() + "\n" + priv.Y.String() + "\n")
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
	log.SetFlags(0)
	log.SetPrefix("keygen: ")
	packing := flag.String("packing", "p256", "packing name, such as p256")
	curve := []elliptic.Curve{16: elliptic.P256(), 18: elliptic.P384(), 17: elliptic.P521()}
	flag.Parse()

	p, ok := ee.PackIndex[*packing]
	if !ok {
		log.Fatal("unrecognized packing")
	}
	createKeys(curve[p], p)
}

func sshdir() string {
	user, err := user.Current()
	if err != nil {
		log.Fatal("no user")
	}
	return filepath.Join(user.HomeDir, ".ssh")
}
