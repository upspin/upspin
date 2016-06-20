// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package client

import (
	"crypto/rand"
	"fmt"
	"testing"

	"upspin.io/factotum"
	"upspin.io/log"
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
	p256Public  = upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	p256Private = "82201047360680847258309465671292633303992565667422607675215625927005262185934"
	p521Public  = upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077\n")
	p521Private = "1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"
)

func BenchmarkPut_p256_1byte(b *testing.B)    { benchmarkPutNbyte(b, upspin.EEp256Pack, 1) }
func BenchmarkPut_p256_1kbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEp256Pack, 1024) }
func BenchmarkPut_p256_1Mbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEp256Pack, 1024*1024) }
func BenchmarkPut_p521_1byte(b *testing.B)    { benchmarkPutNbyte(b, upspin.EEp521Pack, 1) }
func BenchmarkPut_p521_1kbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEp521Pack, 1024) }
func BenchmarkPut_p521_1Mbytes(b *testing.B)  { benchmarkPutNbyte(b, upspin.EEp521Pack, 1024*1024) }
func BenchmarkPut_plain_1byte(b *testing.B)   { benchmarkPutNbyte(b, upspin.PlainPack, 1) }
func BenchmarkPut_plain_1kbytes(b *testing.B) { benchmarkPutNbyte(b, upspin.PlainPack, 1024) }
func BenchmarkPut_plain_1Mbytes(b *testing.B) { benchmarkPutNbyte(b, upspin.PlainPack, 1024*1024) }

func benchmarkPutNbyte(b *testing.B, packing upspin.Packing, fileSize int) {
	u := newUserName()
	client, block := setupBench(b, u, packing, fileSize)
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
func setupBench(b *testing.B, userName upspin.UserName, packing upspin.Packing, fileSize int) (upspin.Client, []byte) {
	log.SetLevel(log.Lerror)
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
	var priv string
	switch packing {
	case upspin.EEp256Pack:
		pub = p256Public
		priv = p256Private
	case upspin.EEp521Pack:
		pub = p521Public
		priv = p521Private
	case upspin.PlainPack:
		// Do nothing. Zero key will work.
	default:
		b.Fatalf("No such key for packing: %d", packing)
	}

	context := setup(userName, pub)
	if packing == upspin.EEp521Pack || packing == upspin.EEp256Pack {
		context.Packing = packing
		f, err := factotum.New(pub, priv)
		if err != nil {
			b.Fatal(err)
		}
		context.Factotum = f
	}
	return New(context), block
}
