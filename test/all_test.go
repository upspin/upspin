// Package test contains an integration test for all of upspin.
package test

import (
	"fmt"
	"log"
	"math/rand"
	"testing"

	"upspin.googlesource.com/upspin.git/test/testenv"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

func TestAllInProcess(t *testing.T) {
	for _, packing := range []upspin.Packing{upspin.DebugPack, upspin.PlainPack, upspin.EEp256Pack, upspin.EEp521Pack} {
		env := newEnv(t, packing)
		runAllTests(t, env)
	}
}

var userNameCounter = 0

// newEnv configures a test environment using a packing.
func newEnv(t *testing.T, packing upspin.Packing) *testenv.Env {
	log.Printf("===== Using packing: %d", packing)

	userName := upspin.UserName(fmt.Sprintf("user%d@domain.com", userNameCounter))
	userNameCounter++

	s := &testenv.Setup{
		OwnerName: userName,
		Transport: upspin.InProcess,
		Packing:   packing,
	}
	env, err := testenv.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return env
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

func runAllTests(t *testing.T, env *testenv.Env) {
	testPutGetTopLevelFile(t, env)
	testFileSequentialAccess(t, env)
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
