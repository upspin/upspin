// Package test contains an integration test for all of upspin.
package test

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/client/gcpclient"
	"upspin.googlesource.com/upspin.git/client/testclient"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"

	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/pack/unsafe"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
)

// Config defines the configuration for each test setup.
type Config struct {
	user      upspin.Endpoint
	directory upspin.Endpoint
	store     upspin.Endpoint
	pack      upspin.Packing
}

var (
	// For GCP, start the directory server and store server in
	// ports 8080 and 8081 respectively and set
	// --use_localhost_gcp to true in the command line (see init
	// below)
	useLocalhostGCP = flag.Bool("use_localhost_gcp", false, "set to true to use GCP on the localhost using the default ports (8080 and 8081 for store and directory respectively)")

	inProcess = upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}

	dummyKey  = "a dummy key"
	dummyKeys = upspin.KeyPair{
		Public:  upspin.PublicKey(dummyKey),
		Private: upspin.PrivateKey(dummyKey),
	}
	p521Keys = upspin.KeyPair{
		Public:  upspin.PublicKey("p521\n6450881751971713196569094102081239393076079963958900743928198284492339970336929522903654432965250717230023303429579440002827022968286561560707424665790636516\n6112296178924797905471636976280701727548001722247534805995457563858330724205693643226473079857232632790111053373068566276215130870301334200655705076516179704"),
		Private: upspin.PrivateKey("4521947149785170611891779226481561520929161578837051798262777353868642403465932692054573259457058158914995500557356995179754042449859359445129927600658124810"),
	}
	p256Keys = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n41791600332717317269223890558424257039864678367319112297252443408877177583918\n31135882230954424983906858071404861049739328230080936198766036208628071024704"),
		Private: upspin.PrivateKey("9148895919924802165789297199844745269316784374066786715028616676753697458529"),
	}
)

func TestAll(t *testing.T) {
	if *useLocalhostGCP {
		testAllGCP(t)
	} else {
		testAllInProcess(t)
	}
}

func testAllGCP(t *testing.T) {
	gcpLocalDirectoryEndpoint := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "http://localhost:8081", // default port
	}
	gcpLocalStoreEndpoint := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "http://localhost:8080", // default port
	}

	var configs = []Config{
		{inProcess, gcpLocalDirectoryEndpoint, gcpLocalStoreEndpoint, upspin.DebugPack},
		{inProcess, gcpLocalDirectoryEndpoint, gcpLocalStoreEndpoint, upspin.PlainPack},
		{inProcess, gcpLocalDirectoryEndpoint, gcpLocalStoreEndpoint, upspin.UnsafePack},
		{inProcess, gcpLocalDirectoryEndpoint, gcpLocalStoreEndpoint, upspin.EEp256Pack},
		{inProcess, gcpLocalDirectoryEndpoint, gcpLocalStoreEndpoint, upspin.EEp521Pack},
	}
	for _, config := range configs {
		newGCPSetup(config.user, config.directory, config.store, config.pack).runAllTests(t)
	}
}

func testAllInProcess(t *testing.T) {
	// Tests create a lot of junk so avoid configs that write to permanent storage.
	// The user endpoint should almost certainly point to an ephemeral service.
	var configs = []Config{
		{inProcess, inProcess, inProcess, upspin.DebugPack},
		{inProcess, inProcess, inProcess, upspin.PlainPack},
		{inProcess, inProcess, inProcess, upspin.UnsafePack},
		{inProcess, inProcess, inProcess, upspin.EEp256Pack},
		{inProcess, inProcess, inProcess, upspin.EEp521Pack},
	}

	for _, config := range configs {
		setup, err := newSetup(config.user, config.directory, config.store, config.pack)
		if err != nil {
			t.Error(err)
			continue
		}
		setup.runAllTests(t)
	}
}

// Setup captures the configuration for a test run.
type Setup struct {
	context *upspin.Context
	client  upspin.Client
}

// newSetup allocates and configures a setup for a test run using a testclient as Client.
// The user's name inside the context is set separately using the newUser method.
func newSetup(userEndpoint, dirEndpoint, storeEndpoint upspin.Endpoint, packing upspin.Packing) (*Setup, error) {
	log.Printf("===== Using packing: %d", packing)
	context := newContext(userEndpoint, dirEndpoint, storeEndpoint, packing)
	client, err := testclient.New(context)
	if err != nil {
		return nil, err
	}
	s := &Setup{
		context: context,
		client:  client,
	}
	return s, nil
}

// newGCPSetup allocates and configures a setup for a test run using a gcpclient as Client.
// The user's name inside the context is set separately using the newUser method.
func newGCPSetup(userEndpoint, dirEndpoint, storeEndpoint upspin.Endpoint, packing upspin.Packing) *Setup {
	context := newContext(userEndpoint, dirEndpoint, storeEndpoint, packing)
	s := &Setup{
		context: context,
		client:  gcpclient.New(context),
	}
	return s
}

// newContext allocates and configures a context according to the given endpoints and packing.
// The user's name inside the context is set separately using the newUser method.
func newContext(userEndpoint, dirEndpoint, storeEndpoint upspin.Endpoint, packing upspin.Packing) *upspin.Context {
	context := new(upspin.Context)
	var err error
	context.Packing = packing
	// TODO: order of creation may not be right for some services.
	context.User, err = bind.User(context, userEndpoint)
	if err != nil {
		panic(err)
	}
	context.Store, err = bind.Store(context, storeEndpoint)
	if err != nil {
		panic(err)
	}
	context.Directory, err = bind.Directory(context, dirEndpoint)
	if err != nil {
		panic(err)
	}
	return context
}

var userNameCounter = 0

// newUser installs a new, previously unseen user. This makes it easy for each test to
// have a private space.
func (s *Setup) newUser(t *testing.T) {
	userName := upspin.UserName(fmt.Sprintf("user%d@domain.com", userNameCounter))
	userNameCounter++
	s.context.UserName = userName

	// Set a key depending on the packer type:
	switch s.context.Packing {
	case upspin.EEp256Pack:
		s.context.KeyPair = p256Keys
	case upspin.EEp521Pack:
		s.context.KeyPair = p521Keys
	default:
		s.context.KeyPair = dummyKeys
	}
	testUser := s.context.User.(*testuser.Service)
	// Set the public key for the current user.
	testUser.SetPublicKeys(userName, []upspin.PublicKey{s.context.KeyPair.Public})
	err := testUser.Install(userName, s.context.Directory)                 // TODO: this is a hack.
	if err != nil && !strings.Contains(err.Error(), "already installed") { // TODO: this is a hack.
		panic(err)
	}
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
