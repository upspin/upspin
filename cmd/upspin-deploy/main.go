// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upspin-deploy creates and deploys an Upspin cluster
// on the Google Cloud Platform.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"

	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	container "google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
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

var (
	prefix    = flag.String("prefix", "upspin-", "A `string` that begins all resource names")
	projectID = flag.String("project", "", "Google Cloud Project `ID`")
	zone      = flag.String("zone", "us-central1-a", "Google Cloud `Zone`")
)

func main() {
	flag.Parse()

	if *projectID == "" {
		fmt.Fprintln(os.Stderr, "error: you must specify the -project flag")
		flag.Usage()
		os.Exit(2)
	}

	region := *zone
	if i := strings.LastIndex(*zone, "-"); i > 0 {
		region = (*zone)[:i]
	}
	cfg := Config{
		Prefix:    *prefix,
		ProjectID: *projectID,
		Region:    region,
		Zone:      *zone,
	}

	// TODO: Check that the Google Cloud project exists.
	// TODO: Check that the relevant APIs are enabled.

	if err := wrap("create", cfg.create()); err != nil {
		log.Fatal(err)
	}
	if err := wrap("delete", cfg.delete()); err != nil {
		log.Fatal(err)
	}
}

type Config struct {
	// ProjectID, Region, and Zone specify the Google Cloud
	// project, region, and zone to operate in.
	ProjectID string
	Region    string
	Zone      string

	// Prefix is a string that is used as the prefix for all resource names
	// (buckets, disks, clusters, etc). It may be empty. By varying Prefix
	// one can run multiple Upspin clusters in a single cloud project.
	Prefix string

	// Create/deploy a keyserver (default is to use key.upspin.io).
	KeyServer bool

	// Create/deploy a frontend.
	Frontend bool
}

func (c *Config) create() error {
	count := 0
	errc := make(chan error)

	count++
	go func() {
		if err := wrap("network", c.createNetwork()); err != nil {
			errc <- err
			return
		}
		// Cluster depends on network.
		errc <- wrap("cluster", c.createCluster())
	}()

	count++
	go func() {
		errc <- wrap("buckets", c.createBuckets())
	}()

	count++
	go func() {
		errc <- wrap("addresses", c.createAddresses())
	}()

	count++
	go func() {
		errc <- wrap("disks", c.createDisks())
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

func (c *Config) delete() error {
	count := 0
	errc := make(chan error)

	count++
	go func() {
		// Cluster depends on network, so delete cluster first.
		if err := wrap("cluster", c.deleteCluster()); err != nil {
			errc <- err
			return
		}
		errc <- wrap("network", c.deleteNetwork())
	}()

	count++
	go func() {
		errc <- wrap("buckets", c.deleteBuckets())
	}()

	count++
	go func() {
		errc <- wrap("addresses", c.deleteAddresses())
	}()

	count++
	go func() {
		errc <- wrap("disks", c.deleteDisks())
	}()

	// Wait for the above concurrent tasks to complete.
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			return err
		}
	}

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
	return okReasons(opError(op, err), "alreadyExists")
}

func (c *Config) deleteNetwork() error {
	log.Printf("Deleting network %q", c.networkName())

	svc, err := computeService()
	if err != nil {
		return err
	}

	op, err := svc.Networks.Delete(c.ProjectID, c.networkName()).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.GlobalOperations.Get(c.ProjectID, op.Name).Do()
	}
	return okReasons(opError(op, err), "notFound")

}

func (c *Config) clusterName() string {
	return c.Prefix + "cluster"
}

func (c *Config) createCluster() error {
	log.Printf("Creating cluster %q", c.clusterName())

	svc, err := containerService()
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
	return okReasons(err, "alreadyExists")
}

func (c *Config) deleteCluster() error {
	log.Printf("Deleting cluster %q", c.clusterName())

	svc, err := containerService()
	if err != nil {
		return err
	}

	op, err := svc.Projects.Zones.Clusters.Delete(c.ProjectID, c.Zone, c.clusterName()).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.Projects.Zones.Operations.Get(c.ProjectID, c.Zone, op.Name).Do()
	}
	return okReasons(err, "notFound")
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
		err := client.Bucket(name).Create(ctx, c.ProjectID, nil)
		if err := okReasons(err, "conflict"); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) deleteBuckets() error {
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
		log.Printf("Deleting bucket %q", name)
		err := client.Bucket(name).Delete(ctx)
		if err := okReasons(err, "notFound"); err != nil {
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
		if err := okReasons(opError(op, err), "alreadyExists"); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) deleteAddresses() error {
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
		log.Printf("Deleting address %q", name)
		op, err := svc.Addresses.Delete(c.ProjectID, c.Region, name).Do()
		for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
			time.Sleep(1 * time.Second)
			op, err = svc.RegionOperations.Get(c.ProjectID, c.Region, op.Name).Do()
		}
		if err := okReasons(opError(op, err), "notFound"); err != nil {
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
	return okReasons(opError(op, err), "alreadyExists")

}

func (c *Config) deleteDisks() error {
	name := c.diskName("dirserver")
	log.Printf("Deleting disk %q", name)

	svc, err := computeService()
	if err != nil {
		return err
	}

	op, err := svc.Disks.Delete(c.ProjectID, c.Zone, name).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.ZoneOperations.Get(c.ProjectID, c.Zone, op.Name).Do()
	}
	return okReasons(opError(op, err), "notFound")

}

func containerService() (*container.Service, error) {
	client, err := google.DefaultClient(context.Background(), container.CloudPlatformScope)
	if err != nil {
		return nil, err
	}
	return container.New(client)
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

func wrap(s string, err error, okReasons ...string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %v", s, err)
}

func okReasons(err error, reasons ...string) error {
	if e, ok := err.(*googleapi.Error); ok && len(e.Errors) == 1 {
		for _, r := range reasons {
			if e.Errors[0].Reason == r {
				return nil
			}
		}
	}
	return err
}
