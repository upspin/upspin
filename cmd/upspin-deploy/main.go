// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upspin-deploy creates and deploys an Upspin cluster
// on the Google Cloud Platform.
package main

import (
	"context"
	"errors"
	"log"
	"time"

	"cloud.google.com/go/storage"

	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	container "google.golang.org/api/container/v1"
	"google.golang.org/api/option"
)

// The user must first:
// - Create a Google Cloud Project: https://cloud.google.com/iam-admin/projects
//   - Make sure it has an associated Billing Account.
// - Enable the Compute Engine API
//   https://cloud.google.com/apis/api/compute_component/overview
// - Enable the Container Engine API
//   https://cloud.google.com/apis/api/container/overview
// - Enable the Container Builder API
//   https://cloud.google.com/apis/api/cloudbuild.googleapis.com/overview

func main() {
	// TODO: Check bthat the Google Cloud project exists.
	// TODO: Check that the relevant APIs are enabled.
	cfg := Config{
		Prefix:    "upspin-",
		ProjectID: "upspin-adg",
		Region:    "us-central1",
		Zone:      "us-central1-a",
	}
	if err := cfg.create(); err != nil {
		log.Fatal(err)
	}
}

type Config struct {
	Prefix    string // used for all resources
	ProjectID string
	Region    string
	Zone      string

	// Create/deploy a keyserver (default is to use key.upspin.io).
	KeyServer bool

	// Create/deploy a frontend.
	Frontend bool
}

func (c *Config) create() error {
	errc := make(chan error)
	count := 0

	count++
	go func() {
		if err := c.createNetwork(); err != nil {
			errc <- err
			return
		}
		// Cluster depends on network.
		errc <- c.createCluster()
	}()

	count++
	go func() {
		errc <- c.createBuckets()
	}()

	count++
	go func() {
		errc <- c.createAddresses()
	}()

	count++
	go func() {
		errc <- c.createDisks()
	}()

	// Wait for the above concurrent tasks to complete.
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			return err
		}
	}

	// Generate dirserver key, if none specified.

	// Build Docker base image.

	return nil
}

func (c *Config) deploy() error {
	return nil
}

func (c *Config) networkName() string {
	return c.Prefix + "network"
}

func (c *Config) createNetwork() error {
	log.Printf("Creating network %q", c.networkName())

	svc, err := computeService()
	if err != nil {
		return err
	}

	network := &compute.Network{
		Name: c.networkName(),
		AutoCreateSubnetworks: true,
	}
	op, err := svc.Networks.Insert(c.ProjectID, network).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.GlobalOperations.Get(c.ProjectID, op.Name).Do()
	}
	return opError(op, err)
}

func (c *Config) clusterName() string {
	return c.Prefix + "cluster"
}

func (c *Config) createCluster() error {
	log.Printf("Creating cluster %q", c.clusterName())

	client, err := google.DefaultClient(context.Background(), container.CloudPlatformScope)
	if err != nil {
		return err
	}
	svc, err := container.New(client)
	if err != nil {
		return err
	}

	req := &container.CreateClusterRequest{
		Cluster: &container.Cluster{
			Name:             c.clusterName(),
			Network:          c.networkName(),
			InitialNodeCount: 2,
			NodeConfig: &container.NodeConfig{
				DiskSizeGb:  10,
				MachineType: "n1-standard-1",
				OauthScopes: []string{
					// Required for mounting persistent disk.
					"https://www.googleapis.com/auth/compute",
					// Required to read/write to cloud storage.
					"https://www.googleapis.com/auth/devstorage.read_write",
					// Required to write log files.
					"https://www.googleapis.com/auth/logging.write",
					// Required to write metrics.
					"https://www.googleapis.com/auth/monitoring.write",
				},
			},
		},
	}
	op, err := svc.Projects.Zones.Clusters.Create(c.ProjectID, c.Zone, req).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.Projects.Zones.Operations.Get(c.ProjectID, c.Zone, op.Name).Do()
	}
	return err
}

var bucketSuffixes = []string{
	"letsencrypt",
	"store",
	"key",
}

func (c *Config) bucketName(suffix string) string {
	// Use the Project ID in the bucket name
	// as buckets are not project-scoped.
	return c.ProjectID + "-" + c.Prefix + suffix

}

func (c *Config) createBuckets() error {
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithScopes(storage.ScopeFullControl))
	if err != nil {
		return err
	}

	for _, s := range bucketSuffixes {
		if !c.KeyServer && s == "key" {
			continue
		}
		name := c.bucketName(s)
		log.Printf("Creating bucket %q", name)
		if err := client.Bucket(name).Create(ctx, c.ProjectID, nil); err != nil {
			return err
		}
	}
	return nil
}

var addressSuffixes = []string{
	"dirserver",
	"storeserver",
	"keyserver",
	"frontend",
}

func (c *Config) addressName(suffix string) string {
	return c.Prefix + suffix
}

func (c *Config) createAddresses() error {
	svc, err := computeService()
	if err != nil {
		return err
	}

	for _, s := range addressSuffixes {
		if !c.KeyServer && s == "keyserver" {
			continue
		}
		if !c.Frontend && s == "frontend" {
			continue
		}
		name := c.addressName(s)
		log.Printf("Creating address %q", name)
		op, err := svc.Addresses.Insert(c.ProjectID, c.Region, &compute.Address{Name: name}).Do()
		for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
			time.Sleep(1 * time.Second)
			op, err = svc.RegionOperations.Get(c.ProjectID, c.Region, op.Name).Do()
		}
		if err := opError(op, err); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) diskName(suffix string) string {
	return c.Prefix + suffix
}

func (c *Config) createDisks() error {
	name := c.diskName("dirserver")
	log.Printf("Creating disk %q", name)

	svc, err := computeService()
	if err != nil {
		return err
	}

	disk := &compute.Disk{
		Name:   name,
		SizeGb: 10,
		Type:   "zones/" + c.Zone + "/diskTypes/pd-ssd",
	}
	op, err := svc.Disks.Insert(c.ProjectID, c.Zone, disk).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.ZoneOperations.Get(c.ProjectID, c.Zone, op.Name).Do()
	}
	return opError(op, err)

}

func computeService() (*compute.Service, error) {
	client, err := google.DefaultClient(context.Background(), compute.ComputeScope)
	if err != nil {
		return nil, err
	}
	return compute.New(client)
}

func opError(op *compute.Operation, err error) error {
	if err != nil {
		return err
	}
	if op == nil || op.Error == nil || len(op.Error.Errors) == 0 {
		return nil
	}
	return errors.New(op.Error.Errors[0].Message)
}
