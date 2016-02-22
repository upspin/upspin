// Keygen creates local files secret.P-256 and public.P-256
// which contain the private and public parts of a keypair.
// Eventually this will be provided by ssh-agent or e2email
// or something else, but we need a minimally usable and
// safe tool for initial testing.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log"
	"os"
	"os/user"
	"path/filepath"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("keygen: ")
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("key not generated: %s\n", err)
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
	_, err = public.WriteString(priv.X.String() + "\n" + priv.Y.String() + "\n")
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

func sshdir() string {
	user, err := user.Current()
	if err != nil {
		log.Fatal("no user")
	}
	return filepath.Join(user.HomeDir, ".ssh")
}
