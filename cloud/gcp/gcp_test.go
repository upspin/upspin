package gcp

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	projectID  = "upspin"
	bucketName = "upspin-test"
)

var (
	client = New(projectID, bucketName, PublicRead)
	// The time is important because we never delete this file, but instead overwrite it.
	testDataStr = fmt.Sprintf("This is test at %v", time.Now())
	testData    = []byte(testDataStr)
	fileName    = "test-file"
)

// This is more of a regression test as it uses the running cloud
// storage in prod. However, since GCP is always available, we accept
// to rely on it.
func TestPutGetAndDownload(t *testing.T) {
	link, err := client.Put(fileName, testData)
	if err != nil {
		t.Fatalf("Can't put: %v", err)
	}
	if !strings.HasPrefix(link, "https://") {
		t.Errorf("Link is not HTTPS")
	}
	retLink, err := client.Get(fileName)
	if err != nil {
		t.Fatalf("Can't get: %v", err)
	}
	if retLink != link {
		t.Errorf("Not the same link as stored: %v vs received: %v", link, retLink)
	}
	resp, err := http.Get(retLink)
	if err != nil {
		t.Errorf("Couldn't get link: %v", err)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Can't read HTTP body: %v", err)
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

func TestPutLocalFile(t *testing.T) {
	// Create a temporary local file.
	f, err := ioutil.TempFile("", "test-gcp-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	localName := f.Name()
	defer os.Remove(localName)
	n, err := f.Write(testData)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(testData) {
		t.Fatalf("Expected to write %d bytes, got %d", len(testData), n)
	}
	// Put to GCP
	const testFileName = "a-new-name"
	link, err := client.PutLocalFile(localName, testFileName)
	if err != nil {
		t.Fatal(err)
	}
	// Check that we get the same link back from Get.
	retLink, err := client.Get(testFileName)
	if err != nil {
		t.Fatal(err)
	}
	if retLink != link {
		t.Errorf("Not the same link as stored: %v vs received: %v", link, retLink)
	}
	resp, err := http.Get(retLink)
	if err != nil {
		t.Fatal(err)
	}
	// Download contents
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Can't read HTTP body: %v", err)
	}
	if string(data) != testDataStr {
		t.Errorf("Expected %q got %q", testDataStr, string(data))
	}
	// Clean up
	err = client.Delete(testFileName)
	if err != nil {
		t.Fatal(err)
	}
}

func TestList(t *testing.T) {
	_, err := client.Put(fileName, testData)
	if err != nil {
		t.Fatal(err)
	}
	names, links, err := client.List("test-f") // prefix for "test-file" above
	if err != nil {
		t.Fatalf("Error in client.List: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(names))
	}
	expectedName := "test-file"
	if names[0] != expectedName {
		t.Errorf("Expected file name %v, got %v", expectedName, names[0])
	}
	if !strings.HasPrefix(links[0], "https://") {
		t.Errorf("Expected download link prefix https:// prefix, got %v", links[0])
	}
}

func TestDelete(t *testing.T) {
	_, err := client.Put(fileName, testData)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Delete(fileName)
	if err != nil {
		t.Fatalf("Expected no errors, got %v", err)
	}
	// Test the side effect after Delete.
	_, err = client.Get(fileName)
	if err == nil {
		t.Fatal("Expected an error, but got none")
	}
}
