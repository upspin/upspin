package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"log"
	"os"
)

func main() {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fmt.Println("key not generated: %s", err)
	}
	private, err := os.Create("secret." + priv.Params().Name)
	if err != nil {
		log.Fatal(err)
	}
	defer private.Close()
	public, err := os.Create("public." + priv.Params().Name)
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
