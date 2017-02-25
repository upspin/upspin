package b2cs

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"upspin.io/cloud/storage"
	"upspin.io/log"
)

const defaultTestBucketName = "upspin-test-scratch"

var (
	client      storage.Storage
	testDataStr = fmt.Sprintf("This is test at %v", time.Now())
	testData    = []byte(testDataStr)
	fileName    = fmt.Sprintf("test-file-%d", time.Now().Second())
	testBucket  = flag.String("test_bucket", defaultTestBucketName, "bucket name to use for testing")
	useB2CS     = flag.Bool("use_b2cs", false, "enable to run b2cs tests; requires Backblaze credentials")
)

// This is more of a regression test as it uses the running cloud
// storage in prod. However, since B2 is always available, we accept
// to rely on it.

func TestPutGetAndDownload(t *testing.T) {
	err := client.Put(fileName, testData)
	if err != nil {
		t.Fatalf("Can't put: %v", err)
	}
	data, err := client.Download(fileName)
	if err != nil {
		t.Fatalf("Can't Download: %v", err)
	}
	if string(data) != testDataStr {
		t.Errorf("Expected %q got %q", testDataStr, string(data))
	}
	// Check that Download yields the same data
	bytes, err := client.Download(fileName)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != testDataStr {
		t.Errorf("Expected %q got %q", testDataStr, string(bytes))
	}
}

func TestDelete(t *testing.T) {
	err := client.Put(fileName, testData)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Delete(fileName)
	if err != nil {
		t.Fatalf("Expected no errors, got %v", err)
	}
	// Test the side effect after Delete.
	_, err = client.Download(fileName)
	if err == nil {
		t.Fatal("Expected an error, but got none")
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !*useB2CS {
		log.Printf(`
cloud/storage/b2cs: skipping test as it requires B2 Cloud Storage access. To
enable this test, ensure you are properly authorized to upload to an B2 Cloud
Storage bucket named by flag -test_bucket and then set this test's flag
-use_b2cs.

`)
		os.Exit(0)
	}
	// Create client that writes to test bucket.
	var err error
	client, err = storage.Dial("B2CS",
		storage.WithKeyValue("b2csBucketName", *testBucket),
		storage.WithKeyValue("defaultACL", Private))
	if err != nil {
		log.Fatalf("cloud/storage/b2cs: couldn't set up client: %v", err)
	}
	// if err := client.(*b2csImpl).createBucket(); err != nil {
	// 	log.Printf("cloud/storage/b2cs: createBucket failed: %v", err)
	// }
	code := m.Run()
	// Clean up.
	// if err := client.(*b2csImpl).deleteBucket(); err != nil {
	// 	log.Printf("cloud/storage/b2cs: deleteBucket failed: %v", err)
	// }
	os.Exit(code)
}
