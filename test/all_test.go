// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package test contains an integration test for all of upspin.
package test

import (
	"fmt"
	"log"
	"math/rand"
	"testing"

	"upspin.io/test/testenv"
	"upspin.io/upspin"

	_ "upspin.io/directory/inprocess"
	_ "upspin.io/pack/debug"
	_ "upspin.io/pack/ee"
	_ "upspin.io/pack/plain"
	_ "upspin.io/store/inprocess"
)

// These variables and consts are used by access_test and integration_test. Don't change them here without updating
// their use there. Also, the keys are registered in the public User service.
const (
	ownersName  = "upspin-test@google.com"
	readersName = "upspin-friend-test@google.com"
)

var (
	keyStore = map[upspin.UserName]map[string]testenv.KeyPair{
		// These keys belong to a fictitious user called upspin-test@google.com. If they're changed here, please upload
		// the public ones to the keyserver on upspin.io:5582
		upspin.UserName(ownersName): {
			"p256": testenv.KeyPair{
				Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n"),
				Private: "82201047360680847258309465671292633303992565667422607675215625927005262185934",
			},
			"p521": testenv.KeyPair{
				Public:  upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077\n"),
				Private: "1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673",
			},
		},
		// These keys belong to a fictitious user called upspin-friend-test@google.com. If they're changed here, please upload
		// the public ones to the keyserver on upspin.io:5582
		upspin.UserName(readersName): {
			"p256": testenv.KeyPair{
				Public:  upspin.PublicKey("p256\n71924488370379946818987229050653820040970171638729570985826507440590282890744\n98209510739612452175889722244428941929387851511059412496741067489708636153322\n"),
				Private: "12667847114690182845907216480350218830765432137334449282204959715092837120411",
			},
			"p521": testenv.KeyPair{
				Public:  upspin.PublicKey("p521\n2643001105868003675926049325704617019265179126928441834645671619583516410960891493660927398876053154544151112732933356768611755942887405372686523401816941574\n3560426880890398995631627239298948247479014271327942974767499548737175268654105044733985540744525774067281139125321728171977030814238770556976354027688285036\n"),
				Private: "5057984701873272519870227974872471453690247866240329178783338629835976725773397318882952593745650429588822150314623238062991886924363530414996690607715952076",
			},
		},
	}
)

func TestAllInProcess(t *testing.T) {
	for _, packing := range []upspin.Packing{upspin.DebugPack, upspin.PlainPack, upspin.EEPack} {
		runAllTests(t, packing)
	}
}

var userNameCounter = 0

// newEnv configures a test environment using a packing.
func newEnv(t *testing.T, packing upspin.Packing) *testenv.Env {
	log.Printf("===== Using packing: %d", packing)

	userName := newUserName()

	s := &testenv.Setup{
		OwnerName: userName,
		Transport: upspin.InProcess,
		Packing:   packing,
		KeyKind:   "p256",
	}
	env, err := testenv.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func newUserName() upspin.UserName {
	userNameCounter++
	return upspin.UserName(fmt.Sprintf("user%d@domain.com", userNameCounter))
}

func setupFileIO(fileName upspin.PathName, max int, env *testenv.Env, t *testing.T) (upspin.File, []byte) {
	f, err := env.Client.Create(fileName)
	if err != nil {
		t.Fatal("create file:", err)
	}

	// Create a data set with each byte equal to its offset.
	data := make([]byte, max)
	for i := range data {
		data[i] = uint8(i)
	}
	return f, data
}

func runAllTests(t *testing.T, packing upspin.Packing) {
	env := newEnv(t, packing)
	testPutGetTopLevelFile(t, env)
	testFileSequentialAccess(t, env)
	testReadAccess(t, packing)
}

func testPutGetTopLevelFile(t *testing.T, env *testenv.Env) {
	client := env.Client
	userName := env.Setup.OwnerName

	fileName := upspin.PathName(userName + "/" + "file")
	const text = "hello sailor"
	_, err := client.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal("put file:", err)
	}
	data, err := client.Get(fileName)
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(data) != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, text)
	}
}

func testFileSequentialAccess(t *testing.T, env *testenv.Env) {
	client := env.Client
	userName := env.Setup.OwnerName

	const Max = 100 * 1000 // Must be > 100.
	fileName := upspin.PathName(userName + "/" + "file")
	f, data := setupFileIO(fileName, Max, env, t)

	// Write the file in randomly sized chunks until it's full.
	for offset, length := 0, 0; offset < Max; offset += length {
		// Pick a random length.
		length = rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.Write(data[offset : offset+length])
		if err != nil {
			t.Fatalf("Write(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("Write length failed: offset %d expected %d got %d", offset, length, n)
		}
	}
	err := f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Now read it back with a similar scan.
	f, err = client.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, Max)
	for offset, length := 0, 0; offset < Max; offset += length {
		length = rand.Intn(Max / 100)
		if offset+length > Max {
			length = Max - offset
		}
		n, err := f.Read(buf[offset : offset+length])
		if err != nil {
			t.Fatalf("Read(offset %d length %d): %v", offset, length, err)
		}
		if n != length {
			t.Fatalf("Read length failed: offset %d expected %d got %d", offset, length, n)
		}
		for i := offset; i < offset+length; i++ {
			if buf[i] != data[i] {
				t.Fatalf("Read at %d (%#x): expected %#.2x got %#.2x", i, i, data[i], buf[i])
			}
		}
	}
}
