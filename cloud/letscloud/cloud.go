// Package letscloud provides a mechanism for caching letsencrypt data
// in Google Cloud Storage. It is for use with https://rsc.io/letsencrypt
//
// Setting up signed URLs
//
// This package uses signed Cloud Storage URLs to read from and write to a
// Google Cloud Storage bucket. The following instructions describe how to
// set up a bucket, a service account for accessing the bucket, and a
// pair of signed URLS to GET and PUT a specific object in that bucket.
//
// Unless you know what you are doing, it is recommended that you create a
// new bucket specifically for letsencrypt cache files. This will ensure
// that you use the default (secure) ACLs for the bucket, and protect your
// TLS certificates from being stolen.
//
// Create a Service Account to access your bucket
//
//   1. Visit https://console.cloud.google.com/permissions/serviceaccounts
//   2. Click 'Create service account'.
//   3. Give it a name (eg "myproject-letsencrypt").
//   4. Check 'Furnish a new private key' (and leave 'Key type' as 'JSON').
//   5. Click 'Create'.
//   6. Put the downloaded private key somewhere safe and note its ID.
//
// Create a Cloud Storage bucket for your letsencrypt cache
//
//   1. Visit https://console.cloud.google.com/storage/browser
//   2. Click 'Create bucket'.
//   3. Give it a name (eg "myproject-letsencrypt").
//   4. Click 'Create'.
//   5. You're now looking at your new bucket. Click 'Buckets' to go back.
//   6. Click the icon to the right of the new bucket,
//      and click 'Edit bucket permissions'.
//   7. Click 'Add item' and add a 'User' with the Service Account ID and
//      'Owner' permissions.
//   8. Click 'Save'.
//
// Create signed GET and PUT URLs
//
//   1. Install the Google Cloud SDK: https://cloud.google.com/sdk/
//   2. Create the signed PUT URL:
//      $ gsutil signurl -m PUT -d 3650d /path/to/key.json gs://$BUCKET/$OBJECT
//      (Where $BUCKET is the storage bucket you created, and $OBJECT
//      is some unique string that identifies this particular service.)
//   3. Populate the PUT URL with an empty file:
//      $ curl -H 'Content-length: 0' -X PUT '$PUT_URL'
//      (Where $PUT_URL is the URL that gsutil just printed.)
//   4. Create the signed GET URL:
//      $ gsutil signurl -m PUT -d 3650d /path/to/key.json gs://$BUCKET/$OBJECT
//   5. Make a note of both the PUT and GET URLs for use with this package's
//      Cache function.
//
// Storing the signed URLs securely
//
// If you're using Google Compute Engine, a convenient and secure place
// to store the Signed URLs is in the metadata of your compute instance.
// The following code fetches the signed URLs from the "letscloud-get-url" and
// "letscloud-put-url" instance attributes and sets up a letsencrypt.Manager
// that stores its cache in the object specified by those URLs.
//
// 	var m letsencrypt.Manager
// 	v := func(key string) string {
// 		v, err := metadata.InstanceAttributeValue(key)
// 		if err != nil {
// 			log.Fatalf("Couldn't read %q metadata value: %v", key, err)
// 		}
// 		return v
// 	}
// 	if err := letscloud.Cache(&m, v("letscloud-get-url"), v("letscloud-put-url")); err != nil {
// 		log.Fatal(err)
// 	}
// 	log.Fatal(m.Serve())
//
// (Package metadata is "google.golang.org/cloud/compute/metadata".)
//
package letscloud

// TODO(adg): check bucket and object permissions; refuse if public

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"

	"rsc.io/letsencrypt"
)

// Cache caches letsencrypt data for the given Manager in the Google Cloud
// Storage object identified by the getURL and putURL values.
// See the package comment for details on obtaining these values.
func Cache(m *letsencrypt.Manager, getURL, putURL string) error {
	var data []byte
	r, err := http.Get(getURL)
	if err != nil {
		return fmt.Errorf("letscloud: reading cache: %v", err)
	}
	data, err = ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return fmt.Errorf("letscloud: reading cache: %v", err)
	}
	if r.StatusCode == http.StatusOK && len(data) > 0 {
		if err := m.Unmarshal(string(data)); err != nil {
			return fmt.Errorf("letscloud: reading cache: %v", err)
		}
	}

	go func() {
		for range m.Watch() {
			req, err := http.NewRequest("PUT", putURL, strings.NewReader(m.Marshal()))
			if err != nil {
				log.Printf("letscloud: writing cache: %v", err)
				continue
			}
			r, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Printf("letscloud: writing cache: %v", err)
				continue
			}
			if r.StatusCode != http.StatusOK {
				log.Printf("letscloud: writing cache: %v", r.Status)
			}
		}
	}()

	return nil
}
