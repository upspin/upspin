// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upspin-deploy creates and deploys an Upspin cluster
// on the Google Cloud Platform.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// - Authenticate using the gcloud tool:
//   $ gcloud auth login

var (
	project = flag.String("project", "", "Google Cloud Project `ID`")
	zone    = flag.String("zone", "us-central1-a", "Google Cloud `Zone`")

	prefix = flag.String("prefix", "", "A `string` that begins all resource names")
	domain = flag.String("domain", "", "The base domain `name` for the Upspin services")

	keyserver = flag.Bool("keyserver", false, "Create/deploy/delete a keyserver in addition to the usual set")
	frontend  = flag.Bool("frontend", false, "Create/deploy/delete a frontend in addition to the usual set")

	create = flag.Bool("create", false, "Create cloud services")
	deploy = flag.Bool("deploy", true, "Deploy Upspin servers")
	delete = flag.String("delete", "", "Delete cloud services (string must equal the project name, for safety)")
)

const defaultKeyServerEndpoint = "remote,key.upspin.io:443"

func main() {
	flag.Parse()

	if *project == "" {
		fmt.Fprintln(os.Stderr, "error: you must specify the -project flag")
		flag.Usage()
		os.Exit(2)
	}

	// Region is determined from the given zone.
	region := *zone
	if i := strings.LastIndex(*zone, "-"); i > 0 {
		region = (*zone)[:i]
	}

	cfg := Config{
		Project: *project,
		Region:  region,
		Zone:    *zone,

		Prefix: *prefix,
		Domain: *domain,

		KeyServer: *keyserver,
		Frontend:  *frontend,
	}

	// TODO: Check that the Google Cloud project exists.
	// TODO: Check that the relevant APIs are enabled.

	if *delete != "" {
		if *delete != *project {
			fmt.Fprintln(os.Stderr, "error: -delete must equal -project")
			os.Exit(1)
		}
		if err := wrap("delete", cfg.delete()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *create {
		if err := wrap("create", cfg.create()); err != nil {
			log.Fatal(err)
		}
	}
	if *deploy {
		if err := wrap("deploy", cfg.deploy()); err != nil {
			log.Fatal(err)
		}
	}
}

type Config struct {
	// Project, Region, and Zone specify the Google Cloud
	// project, region, and zone to operate in.
	Project string
	Region  string
	Zone    string

	// Prefix is a string that is used as the prefix for all resource names
	// (buckets, disks, clusters, etc). It may be empty. By varying Prefix
	// one can run multiple Upspin clusters in a single cloud project.
	Prefix string

	// Domain specifies the IANA domain under which these services will
	// run. For example, if it is set to "example.com" then the storeserver
	// will be "store.example.com".
	Domain string

	// Create/deploy a keyserver (default is to use key.upspin.io).
	KeyServer bool

	// Create/deploy a frontend.
	Frontend bool
}

func (c *Config) create() error {
	log.Printf("Creating cloud services: Project=%q Zone=%q Prefix=%q", c.Project, c.Zone, c.Prefix)

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

	count++
	go func() {
		errc <- wrap("base", c.buildBaseImage())
	}()

	// Wait for the above concurrent tasks to complete.
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			return err
		}
	}

	// Generate dirserver key, if none specified.

	return nil
}

func (c *Config) buildBaseImage() error {
	log.Println("Building base Docker image")
	return cdbuild(repoPath("cloud/docker/base"), c.Project, "base")
}

func (c *Config) delete() error {
	log.Printf("Deleting cloud services: Project=%q Zone=%q Prefix=%q", c.Project, c.Zone, c.Prefix)

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
	log.Printf("Deploying Upspin servers: Project=%q Zone=%q Prefix=%q", c.Project, c.Zone, c.Prefix)

	// Install dependencies to speed builds.
	if err := c.installDeps(); err != nil {
		return wrap("deps", err)
	}

	if err := c.kubeCredentials(); err != nil {
		return wrap("gcloud", err)
	}

	count := 0
	errc := make(chan error)

	for _, s := range c.servers() {
		count++
		go func(s string) {
			if err := wrap(s, c.buildServer(s)); err != nil {
				errc <- err
				return
			}
			errc <- c.deployServer(s)
		}(s)
	}

	// Wait for the above concurrent tasks to complete.
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			return err
		}
	}

	return nil
}

func (c *Config) kubeCredentials() error {
	cmd := exec.Command("gcloud", "--project", c.Project, "container", "clusters", "--zone", c.Zone, "get-credentials", c.clusterName())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) installDeps() error {
	log.Print("Running 'go install' for dependencies")

	cmd := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`,
		"upspin.io/cmd/dirserver",
		"upspin.io/cmd/storeserver",
		"upspin.io/cmd/keyserver",
		"upspin.io/cmd/frontend",
	)
	cmd.Env = c.buildEnv()
	cmd.Stderr = os.Stderr
	depBytes, err := cmd.Output()
	if err != nil {
		return err
	}
	deps := strings.Split(string(bytes.TrimSpace(depBytes)), "\n")

	cmd = exec.Command("go", append([]string{"install"}, deps...)...)
	cmd.Env = c.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) buildBinary(dir, server string) error {
	log.Printf("Building %q", server)
	out := filepath.Join(dir, server)
	pkg := "upspin.io/cmd/" + server
	cmd := exec.Command("go", "build", "-tags", "debug", "-o", out, pkg)
	cmd.Env = c.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) buildEnv() (env []string) {
	for _, s := range os.Environ() {
		if strings.HasPrefix(s, "GOOS=") || strings.HasPrefix(s, "GOARCH=") {
			continue
		}
		env = append(env, s)
	}
	return append(env, "GOOS=linux", "GOARCH=amd64")
}

func (c *Config) buildServer(server string) error {
	dir, err := ioutil.TempDir("", "upspin-deploy")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	var rc []string
	switch server {
	case "dirserver":
		rc = []string{
			"username=" + c.dirServerUserName(),
			"secrets=/upspin",
			"storeserver=" + c.endpoint("store"),
		}
	case "storeserver":
		rc = []string{
			"username=storeserver",
			"secrets=none",
		}
	}
	err = c.writeRC(dir, rc)
	if err != nil {
		return err
	}

	var files []string
	switch server {
	case "dirserver":
		files = []string{
			"public.upspinkey",
			"secret.upspinkey",
			"symmsecret.upspinkey",
		}
	}
	base := filepath.Join(os.Getenv("HOME"), "upspin/deploy", c.Project, server)
	for _, f := range files {
		if err := cp(filepath.Join(dir, f), filepath.Join(base, f)); err != nil {
			return fmt.Errorf("error copying %q for %v: %v", f, server, err)
		}
	}

	if err := c.copyDockerfile(dir, server); err != nil {
		return err
	}
	if err := c.buildBinary(dir, server); err != nil {
		return err
	}

	name := c.Prefix + server
	log.Printf("Building Docker image %q", name)
	if err := cdbuild(dir, c.Project, name); err != nil {
		return err
	}

	return nil
}

func (c *Config) deployServer(server string) error {
	log.Printf("Deploying %q", server)

	// Update deployment.
	cfg, err := ioutil.ReadFile(repoPath("cloud/k8s/deployment/" + server + ".yaml"))
	if err != nil {
		return err
	}
	cfg = c.prepareConfig(cfg, server)
	if err := kubeApply(cfg); err != nil {
		return err
	}

	// Update service.
	cfg, err = ioutil.ReadFile(repoPath("cloud/k8s/service/" + server + ".yaml"))
	if err != nil {
		return err
	}
	cfg = c.prepareConfig(cfg, server)

	ip, err := c.ipAddress(server)
	if err != nil {
		return err
	}
	cfg = bytes.Replace(cfg, []byte("IPADDR"), []byte(ip), -1)

	return kubeApply(cfg)
}

func kubeApply(cfg []byte) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) ipAddress(server string) (string, error) {
	svc, err := computeService()
	if err != nil {
		return "", err
	}
	a, err := svc.Addresses.Get(c.Project, c.Region, c.addressName(server)).Do()
	if err != nil {
		return "", err
	}
	return a.Address, nil
}

func (c *Config) dirServerUserName() string {
	return "upspin-dir@" + c.Domain
}

func (c *Config) writeRC(dir string, extras []string) error {
	var buf bytes.Buffer
	buf.WriteString("keyserver=" + c.keyServerEndpoint() + "\n")
	for _, s := range extras {
		buf.WriteString(s)
		buf.WriteByte('\n')
	}
	return ioutil.WriteFile(filepath.Join(dir, "rc"), buf.Bytes(), 0644)
}

func (c *Config) copyDockerfile(dir, server string) error {
	data, err := ioutil.ReadFile(repoPath("cloud/docker/" + server + "/Dockerfile"))
	if err != nil {
		return err
	}
	data = c.prepareConfig(data, server)
	return ioutil.WriteFile(filepath.Join(dir, "Dockerfile"), data, 0644)
}

func (c *Config) prepareConfig(data []byte, server string) []byte {
	data = bytes.Replace(data, []byte("PREFIX"), []byte(c.Prefix), -1)
	data = bytes.Replace(data, []byte("PROJECT"), []byte(c.Project), -1)

	bucket := ""
	switch server {
	case "keyserver":
		bucket = c.bucketName("key")
	case "storeserver":
		bucket = c.bucketName("store")
	}
	if bucket != "" {
		data = bytes.Replace(data, []byte("BUCKET"), []byte(bucket), -1)
	}

	return data
}

func (c *Config) keyServerEndpoint() string {
	if !c.KeyServer {
		return defaultKeyServerEndpoint
	}
	return c.endpoint("key")
}

func (c *Config) endpoint(server string) string {
	return "remote," + c.hostName(server) + ":443"
}

func (c *Config) hostName(host string) string {
	return host + "." + c.Domain
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
	op, err := svc.Networks.Insert(c.Project, network).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.GlobalOperations.Get(c.Project, op.Name).Do()
	}
	return okReasons(opError(op, err), "alreadyExists")
}

func (c *Config) deleteNetwork() error {
	log.Printf("Deleting network %q", c.networkName())

	svc, err := computeService()
	if err != nil {
		return err
	}

	op, err := svc.Networks.Delete(c.Project, c.networkName()).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.GlobalOperations.Get(c.Project, op.Name).Do()
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
				Metadata: map[string]string{
					"letsencrypt-bucket": c.bucketName("letsencrypt"),
				},
			},
		},
	}
	op, err := svc.Projects.Zones.Clusters.Create(c.Project, c.Zone, req).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.Projects.Zones.Operations.Get(c.Project, c.Zone, op.Name).Do()
	}
	return okReasons(err, "alreadyExists")
}

func (c *Config) deleteCluster() error {
	log.Printf("Deleting cluster %q", c.clusterName())

	svc, err := containerService()
	if err != nil {
		return err
	}

	op, err := svc.Projects.Zones.Clusters.Delete(c.Project, c.Zone, c.clusterName()).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.Projects.Zones.Operations.Get(c.Project, c.Zone, op.Name).Do()
	}
	return okReasons(err, "notFound")
}

var bucketSuffixes = []string{
	"letsencrypt",
	"store",
	"key",
}

func (c *Config) bucketName(suffix string) string {
	// Use the Project  in the bucket name
	// as buckets are not project-scoped.
	return c.Project + "-" + c.Prefix + suffix
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
		err := client.Bucket(name).Create(ctx, c.Project, nil)
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

func (c *Config) servers() (ss []string) {
	for _, s := range []string{
		"dirserver",
		"storeserver",
		"keyserver",
		"frontend",
	} {
		if !c.KeyServer && s == "keyserver" {
			continue
		}
		if !c.Frontend && s == "frontend" {
			continue
		}
		ss = append(ss, s)
	}
	return
}

func (c *Config) addressName(suffix string) string {
	return c.Prefix + suffix
}

func (c *Config) createAddresses() error {
	svc, err := computeService()
	if err != nil {
		return err
	}

	for _, s := range c.servers() {
		name := c.addressName(s)
		log.Printf("Creating address %q", name)
		op, err := svc.Addresses.Insert(c.Project, c.Region, &compute.Address{Name: name}).Do()
		for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
			time.Sleep(1 * time.Second)
			op, err = svc.RegionOperations.Get(c.Project, c.Region, op.Name).Do()
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

	for _, s := range c.servers() {
		name := c.addressName(s)
		log.Printf("Deleting address %q", name)
		op, err := svc.Addresses.Delete(c.Project, c.Region, name).Do()
		for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
			time.Sleep(1 * time.Second)
			op, err = svc.RegionOperations.Get(c.Project, c.Region, op.Name).Do()
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
	op, err := svc.Disks.Insert(c.Project, c.Zone, disk).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.ZoneOperations.Get(c.Project, c.Zone, op.Name).Do()
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

	op, err := svc.Disks.Delete(c.Project, c.Zone, name).Do()
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		op, err = svc.ZoneOperations.Get(c.Project, c.Zone, op.Name).Do()
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

func cp(dst, src string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	fi, err := sf.Stat()
	if err != nil {
		return err
	}
	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()
	// Windows doesn't implement Fchmod.
	if runtime.GOOS != "windows" {
		if err := df.Chmod(fi.Mode()); err != nil {
			return err
		}
	}
	_, err = io.Copy(df, sf)
	if err != nil {
		return err
	}
	if err := df.Close(); err != nil {
		return err
	}
	// Ensure the destination has the same mtime as the source.
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

func repoPath(suffix string) string {
	return filepath.Join(os.Getenv("GOPATH"), "src/upspin.io", suffix)
}
