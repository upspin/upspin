// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The Upspin-setupstorage comamnd is an external upspin subcommand that
// executes the second step in establishing an upspinserver.
// Run upspin setupstorage -help for more information.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/oauth2/google"
	iam "google.golang.org/api/iam/v1"
	storage "google.golang.org/api/storage/v1"

	"upspin.io/flags"
	"upspin.io/subcmd"
)

type state struct {
	*subcmd.State
}

const help = `
Setupstorage is the second step in establishing an upspinserver,
It sets up cloud storage for your Upspin installation. You may skip this step
if you wish to store Upspin data on your server's local disk.
The first step is 'setupdomain' and the final step is 'setupserver'.

Setupstorage creates a Google Cloud Storage bucket and a service account for
accessing that bucket. It then writes the service account private key to
$where/$domain/serviceaccount.json and updates the server configuration files
in that directory to use the specified bucket.

Before running this command, you must create a Google Cloud Project and
associated Billing Account using the Cloud Console:
	https://cloud.google.com/console
The project ID can be any available string, but for clarity it's helpful to
pick something that resembles your domain name.

You must also install the Google Cloud SDK:
	https://cloud.google.com/sdk/downloads
Authenticate and enable the necessary APIs:
	$ gcloud auth login
	$ gcloud --project <project> beta service-management enable iam.googleapis.com storage_api
And, finally, authenticate again in a different way:
	$ gcloud auth application-default login
`

func main() {
	fmt.Printf("%q\n", os.Args)
	const name = "setupstorage"

	log.SetFlags(0)
	log.SetPrefix("upspin setupstorage: ")

	s := &state{
		State: subcmd.NewState(name),
	}

	where := flag.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	domain := flag.String("domain", "", "domain `name` for this Upspin installation")

	flags.Register() // enable all global flags

	s.ParseFlags(flag.CommandLine, os.Args[1:], help,
		"-project=<gcp_project_name> setupstorage -domain=<name> <bucket_name>")
	if flag.NArg() != 1 {
		s.Exitf("a single bucket name must be provided")
	}
	if *domain == "" || flags.Project == "" {
		s.Exitf("the -domain and -project flags must be provided")
	}

	bucket := flag.Arg(0)

	cfgPath := filepath.Join(*where, *domain)
	cfg := s.ReadServerConfig(cfgPath)

	email := s.createServiceAccount(cfgPath)
	fmt.Printf("Service account %q created.\n", email)

	s.createBucket(email, bucket)
	fmt.Printf("Bucket %q created.\n", bucket)

	cfg.Bucket = bucket
	s.WriteServerConfig(cfgPath, cfg)

	fmt.Printf("You should now deploy the upspinserver binary and run 'upspin setupserver'.\n")

	s.Cleanup()
}

func (s *state) createServiceAccount(cfgPath string) (email string) {
	// TODO(adg): detect that key exists and re-use it
	client, err := google.DefaultClient(context.Background(), iam.CloudPlatformScope)
	if err != nil {
		// TODO: ask the user to run 'gcloud auth application-default login'
		s.Exit(err)
	}
	svc, err := iam.New(client)
	if err != nil {
		s.Exit(err)
	}

	// TODO(adg): detect that the account exists
	// and decide what to do in that case
	name := "projects/" + flags.Project
	req := &iam.CreateServiceAccountRequest{
		AccountId: "upspinstorage", // TODO(adg): flag?
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: "Upspin Storage",
		},
	}
	acct, err := svc.Projects.ServiceAccounts.Create(name, req).Do()
	if err != nil {
		s.Exit(err)
	}

	name += "/serviceAccounts/" + acct.Email
	req2 := &iam.CreateServiceAccountKeyRequest{}
	key, err := svc.Projects.ServiceAccounts.Keys.Create(name, req2).Do()
	if err != nil {
		s.Exit(err)
	}

	b, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		s.Exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(cfgPath, "serviceaccount.json"), b, 0600)
	if err != nil {
		s.Exit(err)
	}

	return acct.Email
}

func (s *state) createBucket(email, bucket string) {
	client, err := google.DefaultClient(context.Background(), storage.DevstorageFullControlScope)
	if err != nil {
		// TODO: ask the user to run 'gcloud auth application-default login'
		s.Exit(err)
	}
	svc, err := storage.New(client)
	if err != nil {
		s.Exit(err)
	}

	_, err = svc.Buckets.Insert(flags.Project, &storage.Bucket{
		Acl: []*storage.BucketAccessControl{{
			Bucket: bucket,
			Entity: "user-" + email,
			Email:  email,
			Role:   "OWNER",
		}},
		Name: bucket,
		// TODO(adg): flag for location
	}).Do()
	if err != nil {
		s.Exit(err)
	}
}
