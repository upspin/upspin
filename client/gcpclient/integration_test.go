// This is an integration test for gcpclient. It requires a local GCP
// Directory instance running on port 8081. The use 'go test -tags integration'.
//
// The line below is important or 'go test' will fail:

// +build integration

package gcpclient

import (
	"fmt"
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"

	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/pack/unsafe"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
)

const (
	fileContents = "contents"
)

var (
	privateKey16 = upspin.PrivateKey{
		Public:  upspin.PublicKey("104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: []byte("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
	privateKey17 = upspin.PrivateKey{
		Public:  upspin.PublicKey("5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077"),
		Private: []byte("1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"),
	}
	unsafePrivateKey = upspin.PrivateKey{
		Public:  upspin.PublicKey("123456"),
		Private: []byte("123456"),
	}
)

func setupContext(packing upspin.Packing) *upspin.Context {
	context := &upspin.Context{
		Packing: packing,
	}
	// For testing, use an InProcess User and Store...
	inProcessEndpoint := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored
	}
	// and a real GCP directory.
	endpoint := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "http://localhost:8081",
	}

	// TODO: This bootstrapping is fragile and will break. It depends on the order of setup.
	var err error
	context.User, err = access.BindUser(context, inProcessEndpoint)
	if err != nil {
		panic(err)
	}
	context.Store, err = access.BindStore(context, inProcessEndpoint)
	if err != nil {
		panic(err)
	}
	context.Directory, err = access.BindDirectory(context, endpoint)
	if err != nil {
		panic(err)
	}
	return context
}

func setupUser(context *upspin.Context, userName upspin.UserName) {
	testUser := context.User.(*testuser.Service)
	err := testUser.Install(userName, context.Directory)
	if err != nil && !strings.Contains(err.Error(), "already installed") {
		panic(err)
	}
	// For UnsafePack, use the same private and public keys
	switch context.Packing {
	case upspin.EEp256Pack:
		context.PrivateKey = privateKey16
	case upspin.EEp521Pack:
		context.PrivateKey = privateKey17
	default:
		context.PrivateKey = unsafePrivateKey
	}
	context.UserName = userName
	testUser.SetPublicKeys(userName, []upspin.PublicKey{context.PrivateKey.Public})
}

func testMkdir(context *upspin.Context, t *testing.T) {
	setupUser(context, upspin.UserName("foo@bar.com"))
	c := New(context)

	dirPath := upspin.PathName("foo@bar.com/mydir")
	loc, err := c.MakeDirectory(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	var zeroLoc upspin.Location
	if loc == zeroLoc {
		t.Errorf("Expected a real location, got zero")
	}
	// Look inside the dir entry to make sure it got created.
	entry, err := context.Directory.Lookup(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != dirPath {
		t.Errorf("Expected %s, got %s", dirPath, entry.Name)
	}
	if !entry.Metadata.IsDir {
		t.Errorf("Expected directory, got non-dir")
	}
}

func testPutAndGet(context *upspin.Context, t *testing.T) {
	setupUser(context, upspin.UserName("foo2@bar.com"))
	c := New(context)

	filePath := upspin.PathName("foo2@bar.com/myfile.txt")
	loc, err := c.Put(filePath, []byte(fileContents))
	if err != nil {
		t.Fatal(err)
	}
	var zeroLoc upspin.Location
	if loc == zeroLoc {
		t.Errorf("Expected a real location, got zero")
	}
	// Now read it back
	data, err := c.Get(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != fileContents {
		t.Errorf("Expected %s, got %s", fileContents, data)
	}
}

func testCreateAndOpen(context *upspin.Context, t *testing.T) {
	setupUser(context, upspin.UserName("foo3@bar.com"))
	c := New(context)

	filePath := upspin.PathName("foo3@bar.com/myotherfile.txt")

	f, err := c.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	n, err := f.Write([]byte(fileContents))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(fileContents) {
		t.Fatalf("Expected to write %d bytes, got %d", len(fileContents), n)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	f, err = c.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 30)
	n, err = f.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(fileContents) {
		t.Fatalf("Expected to read %d bytes, got %d", len(fileContents), n)
	}
	buf = buf[:n]
	if string(buf) != fileContents {
		t.Errorf("Expected to read %q, got %q", fileContents, buf)
	}
}

func testGlob(context *upspin.Context, t *testing.T) {
	setupUser(context, upspin.UserName("foo4@bar.com"))
	c := New(context)

	for i := 0; i <= 10; i++ {
		dirPath := upspin.PathName(fmt.Sprintf("foo4@bar.com/mydir%d", i))
		_, err := c.MakeDirectory(dirPath)
		if err != nil {
			t.Fatal(err)
		}

	}
	dirEntries, err := c.Glob("foo4@bar.com/mydir[0-1]*")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirEntries) != 3 {
		t.Fatalf("Expected 3 paths, got %d", len(dirEntries))
	}
	if string(dirEntries[0].Name) != "foo4@bar.com/mydir0" {
		t.Errorf("Expected mydir0, got %s", dirEntries[0].Name)
	}
	if string(dirEntries[1].Name) != "foo4@bar.com/mydir1" {
		t.Errorf("Expected mydir1, got %s", dirEntries[1].Name)
	}
	if string(dirEntries[2].Name) != "foo4@bar.com/mydir10" {
		t.Errorf("Expected mydir10, got %s", dirEntries[2].Name)
	}
}

func runAllTests(context *upspin.Context, t *testing.T) {
	testMkdir(context, t)
	testPutAndGet(context, t)
	testCreateAndOpen(context, t)
	testGlob(context, t)
}

func TestRunAllTests(t *testing.T) {
	for _, packing := range []upspin.Packing{
		upspin.UnsafePack,
		upspin.PlainPack,
		upspin.DebugPack,
		upspin.EEp256Pack,
		upspin.EEp521Pack,
	} {
		log.Printf("==== Using packing type %d ...", packing)
		context := setupContext(packing)
		runAllTests(context, t)
	}
}
