// Package test contains an integration test for all of upspin.
package test

import (
	"fmt"
	"log"
	"math/rand"
	"testing"

	"upspin.googlesource.com/upspin.git/test/testsetup"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

func TestAll(t *testing.T) {
	testAllInProcess(t)
}

func testAllInProcess(t *testing.T) {
	for _, packing := range []upspin.Packing{upspin.DebugPack, upspin.PlainPack, upspin.EEp256Pack, upspin.EEp521Pack} {
		setup := newSetup(t, packing)
		setup.runAllTests(t)
	}
}

// Setup captures the configuration for a test run.
type Setup struct {
	context *upspin.Context
	client  upspin.Client
	packing upspin.Packing
}

// newSetup allocates and configures a setup for a test run using a packing.
func newSetup(t *testing.T, packing upspin.Packing) *Setup {
	log.Printf("===== Using packing: %d", packing)
	s := &Setup{
		packing: packing,
	}
	return s
}

var userNameCounter = 0

// newUser installs a new, previously unseen user. This makes it easy for each test to
// have a private space.
func (s *Setup) newUser(t *testing.T) {
	userName := upspin.UserName(fmt.Sprintf("user%d@domain.com", userNameCounter))
	userNameCounter++
	var err error
	s.context, err = testsetup.NewContextForUser(userName, s.packing)
	if err != nil {
		t.Fatal(err)
	}
	s.client, err = testsetup.InProcess(s.context)
	if err != nil {
		t.Fatal(err)
	}
	testsetup.InstallUserRoot(s.context)
}

func (s *Setup) setupFileIO(fileName upspin.PathName, max int, t *testing.T) (upspin.File, []byte) {
	f, err := s.client.Create(fileName)
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

func (s *Setup) runAllTests(t *testing.T) {
	s.TestPutGetTopLevelFile(t)
	s.TestFileSequentialAccess(t)
}

func (s *Setup) TestPutGetTopLevelFile(t *testing.T) {
	s.newUser(t)

	fileName := upspin.PathName(s.context.UserName + "/" + "file")
	const text = "hello sailor"
	_, err := s.client.Put(fileName, []byte(text))
	if err != nil {
		t.Fatal("put file:", err)
	}
	data, err := s.client.Get(fileName)
	if err != nil {
		t.Fatal("get file:", err)
	}
	if string(data) != text {
		t.Fatalf("get of %q has text %q; should be %q", fileName, data, text)
	}
}

func (s *Setup) TestFileSequentialAccess(t *testing.T) {
	s.newUser(t)

	const Max = 100 * 1000 // Must be > 100.
	fileName := upspin.PathName(s.context.UserName + "/" + "file")
	f, data := s.setupFileIO(fileName, Max, t)

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
	f, err = s.client.Open(fileName)
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
