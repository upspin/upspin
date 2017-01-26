// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/base64"
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"

	"golang.org/x/oauth2/google"
	iam "google.golang.org/api/iam/v1"
	storage "google.golang.org/api/storage/v1"

	"upspin.io/flags"
)

func (s *State) setupstorage(args ...string) {
	const (
		help = `
setupstorage
`
	)
	fs := flag.NewFlagSet("setupstorage", flag.ExitOnError)
	where := fs.String("where", filepath.Join(os.Getenv("HOME"), "upspin", "deploy"), "`directory` to store private configuration files")
	domain := fs.String("domain", "", "`domain` name")
	s.parseFlags(fs, args, help, "-project=<gcp_project_name> setupstorage -domain=<name> my-bucket")
	if *domain == "" || flags.Project == "" || fs.NArg() != 1 {
		fs.Usage()
	}
	bucket := fs.Arg(0)
	cfgPath := filepath.Join(*where, *domain)

	cfg := s.readServerConfig(cfgPath)

	email := s.createServiceAccount(cfgPath)
	s.createBucket(email, bucket)

	cfg.Bucket = bucket
	s.writeServerConfig(cfgPath, cfg)
}

func (s *State) createServiceAccount(cfgPath string) (email string) {
	// TODO(adg): detect that key exists and re-use it
	client, err := google.DefaultClient(context.Background(), iam.CloudPlatformScope)
	if err != nil {
		// TODO: ask the user to run 'gcloud auth application-default login'
		s.exit(err)
	}
	svc, err := iam.New(client)
	if err != nil {
		s.exit(err)
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
		s.exit(err)
	}

	name += "/serviceAccounts/" + acct.Email
	req2 := &iam.CreateServiceAccountKeyRequest{}
	key, err := svc.Projects.ServiceAccounts.Keys.Create(name, req2).Do()
	if err != nil {
		s.exit(err)
	}

	b, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		s.exit(err)
	}
	err = ioutil.WriteFile(filepath.Join(cfgPath, "serviceaccount.json"), b, 0600)
	if err != nil {
		s.exit(err)
	}

	return acct.Email
}

func (s *State) createBucket(email, bucket string) {
	client, err := google.DefaultClient(context.Background(), storage.DevstorageFullControlScope)
	if err != nil {
		// TODO: ask the user to run 'gcloud auth application-default login'
		s.exit(err)
	}
	svc, err := storage.New(client)
	if err != nil {
		s.exit(err)
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
		s.exit(err)
	}
}
