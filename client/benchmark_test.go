// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package client

import (
	"crypto/rand"
	"fmt"
	"testing"

	"upspin.io/config"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	// Load some packers
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
)

const (
	userNamePattern = "benchuser%d@upspin.io"
	fileName        = "/file.txt"
)

var (
	joePublic = upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	alyPublic = upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077\n")
)

func BenchmarkPut_p256_1byte(b *testing.B)    { benchmarkPutNbyte(b, upspin.EEPack, "p256", 1) }
func BenchmarkPut_p256_1kbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEPack, "p256", 1024) }
func BenchmarkPut_p256_1Mbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEPack, "p256", 1024*1024) }
func BenchmarkPut_p521_1byte(b *testing.B)    { benchmarkPutNbyte(b, upspin.EEPack, "p521", 1) }
func BenchmarkPut_p521_1kbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEPack, "p521", 1024) }
func BenchmarkPut_p521_1Mbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEPack, "p521", 1024*1024) }
func BenchmarkPut_plain_1byte(b *testing.B)   { benchmarkPutNbyte(b, upspin.PlainPack, "", 1) }
func BenchmarkPut_plain_1kbytes(b *testing.B) { benchmarkPutNbyte(b, upspin.PlainPack, "", 1024) }
func BenchmarkPut_plain_1Mbytes(b *testing.B) { benchmarkPutNbyte(b, upspin.PlainPack, "", 1024*1024) }

func benchmarkPutNbyte(b *testing.B, packing upspin.Packing, curveName string, fileSize int) {
	u := newUserName()
	client, block := setupBench(b, u, packing, curveName, fileSize)
	var err error
	for i := 0; i < b.N; i++ {
		_, err = client.Put(upspin.PathName(u)+fileName, block)
		if err != nil {
			b.Fatal(err)
		}
	}
}

var userNameCount = 0

func newUserName() upspin.UserName {
	userNameCount++
	return upspin.UserName(fmt.Sprintf(userNamePattern, userNameCount))
}

// setupBench returns a new client for the username and packing and a byte slice filled with fileSize random bytes.
func setupBench(b *testing.B, userName upspin.UserName, packing upspin.Packing, curveName string, fileSize int) (upspin.Client, []byte) {
	log.SetLevel("error")
	block := make([]byte, fileSize)
	n, err := rand.Read(block)
	if err != nil {
		b.Fatal(err)
	}
	if n != fileSize {
		b.Fatal("not enough random bytes")
	}
	block = block[:n]

	var pub upspin.PublicKey
	var keyDir string
	switch curveName {
	case "p256":
		keyDir = "key/testdata/joe"
		pub = joePublic
	case "p521":
		keyDir = "key/testdata/aly"
		pub = alyPublic
	case "":
		// Do nothing. Zero key will work for PlainPack.
	default:
		b.Fatalf("No such key for packing: %d", packing)
	}

	cfg := setup(baseCfg, userName, pub)
	if packing == upspin.EEPack {
		cfg = config.SetPacking(cfg, packing)
		f, err := factotum.NewFromDir(testutil.Repo(keyDir))
		if err != nil {
			b.Fatal(err)
		}
		cfg = config.SetFactotum(cfg, f)
	}
	return New(cfg), block
}
