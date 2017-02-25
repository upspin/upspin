// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcs

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

	testBucket = flag.String("test_bucket", defaultTestBucketName, "bucket name to use for testing")
	useGcloud  = flag.Bool("use_gcloud", false, "enable to run google cloud tests; requires gcloud auth login")
)

// This is more of a regression test as it uses the running cloud
// storage in prod. However, since GCP is always available, we accept
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
	if !*useGcloud {
		log.Printf(`

cloud/storage/gcs: skipping test as it requires GCS access. To enable this test,
ensure you are authenticated to a GCP project that has editor permissions to a
GCS bucket named by flag -test_bucket and then set this test's flag -use_gcloud.

`)
		os.Exit(0)
	}

	// Create client that writes to test bucket.
	var err error
	client, err = storage.Dial("GCS",
		storage.WithKeyValue("gcpBucketName", *testBucket),
		storage.WithKeyValue("defaultACL", PublicRead))
	if err != nil {
		log.Fatalf("cloud/storage/gcs: couldn't set up client: %v", err)
	}

	code := m.Run()

	// Clean up.
	const verbose = true
	if err := client.(*gcsImpl).emptyBucket(verbose); err != nil {
		log.Printf("cloud/storage/gcs: emptyBucket failed: %v", err)
	}

	os.Exit(code)
}
