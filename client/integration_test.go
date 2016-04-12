// This is an integration test for Client.
//
// To launch this test: 'go test -tags integration'.
//
// The line below is important or 'go test' will fail:

// +build integration

package client_test

import (
	"fmt"
	"log"
	"strings"
	"testing"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/client"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

const (
	fileContents     = "contents"
	userName         = "upspin-test@google.com"              // not a real GAIA account.
	userToShare      = "upspin-friend-test@google.com"       // not a real GAIA account.
	untrustedUser    = "upspin-not-a-friend-test@google.com" // not a real GAIA account.
	dirAlreadyExists = "directory already exists"
)

var (
	keyMap = map[upspin.UserName]map[upspin.Packing]upspin.KeyPair{
		// These keys belong to a fictitious user called upspin-test@google.com. If they're changed here, please upload
		// the public ones to the keyserver on upspin.io:8082
		upspin.UserName(userName): {
			upspin.EEp256Pack: upspin.KeyPair{
				Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
				Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
			},
			upspin.EEp521Pack: upspin.KeyPair{
				Public:  upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077"),
				Private: upspin.PrivateKey("1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"),
			},
		},
		// These keys belong to a fictitious user called upspin-friend-test@google.com. If they're changed here, please upload
		// the public ones to the keyserver on upspin.io:8082
		upspin.UserName(userToShare): {
			upspin.EEp256Pack: upspin.KeyPair{
				Public:  upspin.PublicKey("p256\n71924488370379946818987229050653820040970171638729570985826507440590282890744\n98209510739612452175889722244428941929387851511059412496741067489708636153322"),
				Private: upspin.PrivateKey("12667847114690182845907216480350218830765432137334449282204959715092837120411"),
			},
			upspin.EEp521Pack: upspin.KeyPair{
				Public:  upspin.PublicKey("p521\n2643001105868003675926049325704617019265179126928441834645671619583516410960891493660927398876053154544151112732933356768611755942887405372686523401816941574\n3560426880890398995631627239298948247479014271327942974767499548737175268654105044733985540744525774067281139125321728171977030814238770556976354027688285036"),
				Private: upspin.PrivateKey("5057984701873272519870227974872471453690247866240329178783338629835976725773397318882952593745650429588822150314623238062991886924363530414996690607715952076"),
			},
		},
	}
)

func newContext(packing upspin.Packing) *upspin.Context {
	context := &upspin.Context{
		Packing: packing,
	}
	return context
}

func setupUser(context *upspin.Context, userName upspin.UserName) {
	// Packing and keys are different things. We need a key for signing HTTPS requests, even if packing is Plain or Debug.
	switch context.Packing {
	case upspin.EEp256Pack, upspin.EEp384Pack, upspin.EEp521Pack:
		context.KeyPair = keyMap[userName][context.Packing]
	default:
		context.KeyPair = keyMap[userName][upspin.EEp256Pack]
	}

	context.UserName = userName

	// For testing, use a test GCP Store...
	endpointStore := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "https://upspin.io:9980",
	}
	// ... and a test GCP directory ...
	endpointDir := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "https://upspin.io:9981",
	}
	// ...  and the real upspin.io User.
	endpointUser := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "https://upspin.io:8082",
	}

	// TODO: This bootstrapping is fragile and will break. It depends on the order of setup.
	var err error
	context.User, err = bind.User(context, endpointUser)
	if err != nil {
		panic(err)
	}
	context.Store, err = bind.Store(context, endpointStore)
	if err != nil {
		panic(err)
	}
	context.Directory, err = bind.Directory(context, endpointDir)
	if err != nil {
		panic(err)
	}

	// Ensure user has root.
	_, err = context.Directory.MakeDirectory(path.Join(upspin.PathName(userName), "/"))
	if err != nil && !strings.Contains(err.Error(), dirAlreadyExists) {
		log.Fatal("Can't create root: %s", err)
	}
}

func testMkdir(context *upspin.Context, t *testing.T) {
	setupUser(context, upspin.UserName(userName))
	c := client.New(context)

	dirPath := path.Join(upspin.PathName(userName), "mydir")
	loc, err := c.MakeDirectory(dirPath)
	if err != nil {
		if !strings.Contains(err.Error(), dirAlreadyExists) {
			t.Fatal(err)
		}
	} else {
		var zeroLoc upspin.Location
		if loc == zeroLoc {
			t.Errorf("Expected a real location, got zero")
		}
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
	setupUser(context, upspin.UserName(userName))
	c := client.New(context)

	filePath := path.Join(upspin.PathName(userName), "myfile.txt")
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
	setupUser(context, upspin.UserName(userName))
	c := client.New(context)

	filePath := path.Join(upspin.PathName(userName), "myotherfile.txt")

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
	setupUser(context, upspin.UserName(userName))
	c := client.New(context)

	for i := 0; i <= 10; i++ {
		dirPath := upspin.PathName(fmt.Sprintf("%s/mydir%d", userName, i))
		_, err := c.MakeDirectory(dirPath)
		log.Printf("mkdir %s: %s", dirPath, err)
		if err != nil && !strings.Contains(err.Error(), dirAlreadyExists) {
			t.Fatal(err)
		}
	}
	dirEntries, err := c.Glob(userName + "/mydir[0-1]*")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirEntries) != 3 {
		t.Fatalf("Expected 3 paths, got %d", len(dirEntries))
	}
	if string(dirEntries[0].Name) != userName+"/mydir0" {
		t.Errorf("Expected mydir0, got %s", dirEntries[0].Name)
	}
	if string(dirEntries[1].Name) != userName+"/mydir1" {
		t.Errorf("Expected mydir1, got %s", dirEntries[1].Name)
	}
	if string(dirEntries[2].Name) != userName+"/mydir10" {
		t.Errorf("Expected mydir10, got %s", dirEntries[2].Name)
	}
}

func testSharing(context *upspin.Context, t *testing.T) {
	const (
		sharedContent = "Hey man, whatup?"
	)
	var (
		sharedDir      = path.Join(upspin.PathName(userName), "mydir")
		sharedFilePath = path.Join(sharedDir, "sharedfile")
	)
	setupUser(context, upspin.UserName(userName))
	c := client.New(context)

	// Put an Access file where no one has access (this forces updating the parent dir with no access).
	_, err := c.Put(path.Join(sharedDir, "Access"), []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	// Put a new file under a previously created dir.
	_, err = c.Put(sharedFilePath, []byte(sharedContent))
	if err != nil {
		t.Fatal(err)
	}
	// Now become the other user and read the file.
	setupUser(context, upspin.UserName(userToShare))

	data, err := c.Get(sharedFilePath)
	// If packing is strong encryption, the Get will fail:
	switch context.Packing {
	case upspin.EEp256Pack, upspin.EEp384Pack, upspin.EEp521Pack:
		if err == nil {
			t.Fatal("Expected Get to fail, but it didn't")
		}
	default:
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != sharedContent {
			t.Errorf("Expected %s, got %s", sharedContent, data)
		}
	}
	// Become the test user once again
	setupUser(context, upspin.UserName(userName))

	// Put an Access file first, giving our friend read access
	accessFile := "r:upspin-friend-test@google.com"
	_, err = c.Put(path.Join(sharedDir, "Access"), []byte(accessFile))
	if err != nil {
		t.Fatal(err)
	}
	// Re-write file, so we wrap keys for our friend.
	_, err = c.Put(sharedFilePath, []byte(sharedContent))
	if err != nil {
		t.Fatal(err)
	}
	// Now become some other user again and verify that he has access now.
	setupUser(context, upspin.UserName(userToShare))
	data, err = c.Get(sharedFilePath)
	// And this should not fail under any packing.
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sharedContent {
		t.Errorf("Expected %s, got %s", sharedContent, data)
	}
}

func runAllTests(context *upspin.Context, t *testing.T) {
	testMkdir(context, t)
	testPutAndGet(context, t)
	testCreateAndOpen(context, t)
	testGlob(context, t)
	testSharing(context, t)
}

func TestRunAllTests(t *testing.T) {
	for _, packing := range []upspin.Packing{
		upspin.PlainPack,
		upspin.DebugPack,
		upspin.EEp256Pack,
		upspin.EEp521Pack,
	} {
		log.Printf("==== Using packing type %d ...", packing)
		context := newContext(packing)
		runAllTests(context, t)
	}
}

func BenchmarkPut(b *testing.B) {
	context := newContext(upspin.PlainPack)
	setupUser(context, upspin.UserName(userName))
	c := client.New(context)
	for i := 0; i < b.N; i++ {
		_, err := c.Put(upspin.PathName(userName+"/a.txt"), []byte("1"))
		if err != nil {
			b.Fatal(err)
		}
	}
}
