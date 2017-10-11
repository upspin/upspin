package openstack

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/rackspace/gophercloud/openstack/objectstorage/v1/containers"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
)

const (
	defaultTestRegion    = "BHS3"
	defaultTestContainer = "upspin-test-container"
)

var (
	client storage.Storage

	objectName     = fmt.Sprintf("test-file-%d", time.Now().Second())
	objectContents = []byte(fmt.Sprintf("This is test at %v", time.Now()))

	testRegion    = flag.String("test_region", defaultTestRegion, "region to use for the test container")
	testContainer = flag.String("test_container", defaultTestContainer, "container to use for testing")

	useOpenstack = flag.Bool("use_openstack", false, "enable to run Openstack tests; requires Openstack credentials")
)

func TestPutAndDownloadFromLinkBase(t *testing.T) {
	err := client.Put(objectName, objectContents)
	if err != nil {
		t.Fatalf("Could not put: %v", err)
	}
	base, err := client.LinkBase()
	if err != nil {
		t.Fatalf("Could not get container base: %v", err)
	}
	response, err := http.Get(base + objectName)
	if err != nil {
		t.Fatalf("Could not get from container base: %v", err)
	}
	storedBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("Could not read response body: %v", err)
	}
	if string(storedBytes) != string(objectContents) {
		t.Fatalf("Downloaded contents do not match, wanted %s and got %s",
			objectContents, string(storedBytes))
	}
}

func TestDownloadMissing(t *testing.T) {
	_, err := client.Download("Something I never uploaded")
	uerr, ok := err.(*errors.Error)
	if !ok {
		t.Fatalf("Expected Upspin error, got %v", err)
	}
	if uerr.Kind != errors.NotExist {
		t.Fatalf("Expected NotExist kind, got: %v", uerr.Kind)
	}
}

func TestPutAndDownload(t *testing.T) {
	err := client.Put(objectName, objectContents)
	if err != nil {
		t.Fatalf("Could not put: %v", err)
	}
	storedBytes, err := client.Download(objectName)
	if err != nil {
		t.Fatalf("Could not download: %v", err)
	}
	if string(storedBytes) != string(objectContents) {
		t.Errorf("Downloaded contents do not match, expected %q got %q",
			string(objectContents), string(storedBytes))
	}
}

func TestPutAndDelete(t *testing.T) {
	err := client.Put(objectName, objectContents)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Delete(objectName)
	if err != nil {
		t.Fatalf("Expected no errors, got %v", err)
	}
	_, err = client.Download(objectName)
	if err == nil {
		t.Fatal("Expected an error, but got none")
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if !*useOpenstack {
		log.Printf(`

cloud/storage/openstack: skipping test as it requires Openstack access. To
enable this test, ensure you are properly authorized to upload to an Openstack
container named by flag -test_container and then set this test's flag
-use_openstack.

`)
		os.Exit(0)
	}

	// Create client that writes to test container.
	var err error
	client, err = storage.Dial(
		"Openstack",
		storage.WithKeyValue("openstackRegion", *testRegion),
		storage.WithKeyValue("openstackContainer", *testContainer),
	)
	if err != nil {
		log.Fatalf("cloud/storage/openstack: couldn't set up client: %v", err)
	}
	if err := client.(*openstackStorage).createContainer(); err != nil {
		log.Fatalf("cloud/storage/openstack: createContainer failed: %v", err)
	}

	exitCode := m.Run()

	// Clean up.
	if err := client.(*openstackStorage).deleteContainer(); err != nil {
		log.Fatalf("cloud/storage/openstack: deleteContainer failed: %v", err)
	}

	os.Exit(exitCode)
}

func (s *openstackStorage) createContainer() error {
	return containers.Create(s.client, s.container, containers.CreateOpts{
		ContainerRead: containerPublicACL,
	}).Err
}

func (s *openstackStorage) deleteContainer() error {
	return containers.Delete(s.client, s.container).Err
}
