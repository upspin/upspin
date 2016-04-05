// Keygen creates local files secret.upspinkey and public.upspinkey in ~/.ssh
// which contain the private and public parts of a keypair.

// Eventually we'll offer something like ssh-agent, but we need
// to start with a usable and safe standalone tool.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"upspin.googlesource.com/upspin.git/cmd/keygen/proquint"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/pack/ee"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	packing = flag.String("packing", "p256", "packing name, such as p256")
	secret  = flag.String("secretseed", "", "128 bit secret seed in proquint format")
	where   = flag.String("where", "", "directory to write keys. If empty, $HOME/.ssh/")
)

func createKeys(pack upspin.Packing) {
	// Pick secret 128 bits.
	// TODO  Consider whether we are willing to ask users to write long seeds for P521.
	b := make([]byte, 16)
	var proquintStr string
	if len(*secret) > 0 {
		if len((*secret)) != 47 || (*secret)[5] != '-' {
			log.Fatalf("expected secret like\n lusab-babad-gutih-tugad.gutuk-bisog-mudof-sakat\n"+
				"not\n %s\nkey not generated", *secret)
		}
		for i := 0; i < 8; i++ {
			binary.BigEndian.PutUint16(b[2*i:2*i+2], proquint.Decode([]byte((*secret)[6*i:6*i+5])))
		}
	} else {
		ee.GenEntropy(b)
		proquints := make([]interface{}, 8)
		for i := 0; i < 8; i++ {
			proquints[i] = proquint.Encode(binary.BigEndian.Uint16(b[2*i : 2*i+2]))
		}
		proquintStr = fmt.Sprintf("-secretseed %s-%s-%s-%s.%s-%s-%s-%s\n", proquints...)
		// Ignore punctuation on input;  this format is just to help the user keep their place.

	}

	keyPair, err := ee.CreateKeys(pack, b)

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

	private.WriteString(string(keyPair.Private))
	public.WriteString(string(keyPair.Public))

	err = private.Close()
	if err != nil {
		log.Fatal(err)
	}
	err = public.Close()
	if err != nil {
		log.Fatal(err)
	}
	if proquintStr != "" {
		fmt.Print(proquintStr)
	}
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("keygen: ")
	flag.Parse()

	p := pack.Lookup(16)
	if p == nil {
		log.Fatal("packers apparently not registered")
	}

	packer := pack.LookupByName(*packing)
	if packer == nil {
		log.Fatalf("unrecognized packing %s", *packing)
	}
	createKeys(packer.Packing())
}

func keydir() string {
	if where != nil && len(*where) > 0 {
		return *where
	}
	home := os.Getenv("HOME")
	if len(home) == 0 {
		log.Fatal("no home directory")
	}
	return filepath.Join(home, ".ssh")
}
