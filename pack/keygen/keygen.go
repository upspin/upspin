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
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("keygen: ")
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("key not generated: %s\n", err)
	}
	private, err := os.Create("secret.upspinkey")
	if err != nil {
		log.Fatal(err)
	}
	defer private.Close()
	public, err := os.Create("public.upspinkey")
	if err != nil {
		log.Fatal(err)
	}
	defer public.Close()
	_, err = private.WriteString(priv.D.String() + "\n")
	if err != nil {
		log.Fatal(err)
	}
	_, err = public.WriteString(priv.X.String() + "\n" + priv.Y.String() + "\n")
	if err != nil {
		log.Fatal(err)
	}
}
