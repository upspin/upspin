// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upspin-deploy creates and deploys an Upspin cluster
// on the Google Cloud Platform.
package main

// TODO(adg): support deploying only a single server (only dirserver, say)

// TODO(adg): comprehensive help/setup text
// TODO(adg): delete load balancers
// TODO(adg): kubectl delete services
// TODO(adg): delete container registry entries
// TODO(adg): only create base image once, check if it exists

// TODO(adg): Check that the Google Cloud project exists.
// TODO(adg): Check that the relevant APIs are enabled.
// TODO(adg): Check that we are authenticated.

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
	cloudtrace "google.golang.org/api/cloudtrace/v1"
	compute "google.golang.org/api/compute/v1"
	container "google.golang.org/api/container/v1"
	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
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
// - Enable the Cloud DNS API
//   https://pantheon.corp.google.com/apis/api/dns.googleapis.com/overview
// - Enable the Stackdriver Monitoring API
//   https://pantheon.corp.google.com/apis/api/monitoring.googleapis.com/overview
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
	delete = flag.String("delete", "", "Delete cloud services (string must equal the `project` name, for safety)")
)

func main() {
	flag.Parse()

	mustProvideFlag(project, "-project")
	mustProvideFlag(zone, "-zone")

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

	if *delete != "" {
		if *delete != *project {
			fmt.Fprintln(os.Stderr, "error: -delete must equal -project")
			os.Exit(1)
		}
		if err := wrap("delete", cfg.Delete()); err != nil {
			log.Fatal(err)
		}
		return
	}

	// -domain isn't necessary for delete, but it is for create.
	mustProvideFlag(domain, "-domain")
	if *create {
		if err := wrap("create", cfg.Create()); err != nil {
			log.Fatal(err)
		}
	}
	if *deploy {
		if err := wrap("deploy", cfg.Deploy()); err != nil {
			log.Fatal(err)
		}
	}
}

func mustProvideFlag(f *string, name string) {
	if *f == "" {
		fmt.Fprintf(os.Stderr, "error: you must specify the %s flag\n", name)
		flag.Usage()
		os.Exit(2)
	}
}

// Config specifies an Upspin cluster's configuration.
// It may be used to create, deploy to, or delete an Upspin cluster.
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

func (c *Config) inProd() bool {
	// The upspin-prod and upspin-test projects were created
	// before this script was written, and so they use some
	// different names for things. We hard code those special
	// things here to avoid re-creating everything.
	return (c.Project == "upspin-prod" || c.Project == "upspin-test") && c.Prefix == ""
}

const defaultKeyServerEndpoint = "remote,key.upspin.io:443"

func (c *Config) Create() error {
	if c.inProd() {
		// HACK: see the comment on inProd.
		return errors.New("cannot create services in upspin-prod/test")
	}
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
		if err := wrap("addresses", c.createAddresses()); err != nil {
			errc <- err
			return
		}
		errc <- wrap("zone", c.createZone())
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

	return nil
}

func (c *Config) Delete() error {
	if c.inProd() {
		// HACK: see the comment on inProd.
		return errors.New("cannot delete services in upspin-prod/test")
	}
	log.Printf("Deleting cloud services: Project=%q Zone=%q Prefix=%q", c.Project, c.Zone, c.Prefix)

	count := 0
	errc := make(chan error, 20)

	count++
	go func() {
		errc <- wrap("cluster", c.deleteCluster())
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
		errc <- wrap("zone", c.deleteZone())
	}()

	// Wait for the above concurrent tasks to complete.
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			return err
		}
	}

	count = 0
	errc = make(chan error, 20)

	count++
	go func() {
		errc <- wrap("disks", c.deleteDisks())
	}()

	count++
	go func() {
		errc <- wrap("network", c.deleteNetwork())
	}()

	// Wait for the above concurrent tasks to complete.
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			return err
		}
	}

	return nil
}

func (c *Config) Deploy() error {
	log.Printf("Deploying Upspin servers: Project=%q Zone=%q Prefix=%q", c.Project, c.Zone, c.Prefix)

	// Install dependencies to speed builds.
	if err := c.installDeps(); err != nil {
		return wrap("deps", err)
	}

	if err := c.kubeCredentials(); err != nil {
		return wrap("gcloud", err)
	}

	count := 0
	errc := make(chan error, 20)

	for _, s := range c.servers() {
		count++
		go func(s string) {
			if err := wrap(s, c.buildServer(s)); err != nil {
				errc <- err
				return
			}
			if err := wrap(s, c.deployServer(s)); err != nil {
				errc <- err
				return
			}
			errc <- wrap(s, c.restartServer(s))
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

func (c *Config) buildServer(server string) error {
	log.Printf("Building %q", server)
	dir, err := ioutil.TempDir("", "upspin-deploy")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	switch server {
	case "dirserver":
		err = writeRC(dir,
			"username="+c.dirServerUserName(),
			"secrets=/upspin",
			"keyserver="+c.endpoint("keyserver"),
			"storeserver="+c.endpoint("storeserver"),
			"dirserver=remote,"+c.endpoint("dirserver"),
		)
	case "storeserver":
		err = writeRC(dir,
			"username=storeserver",
			"secrets=none",
			"keyserver="+c.endpoint("keyserver"),
		)
	}
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
	cfg, err := ioutil.ReadFile(repoPath("cloud/kube/deployment/" + server + ".yaml"))
	if err != nil {
		return err
	}
	cfg = c.prepareConfig(cfg, server)
	if err := kubeApply(cfg); err != nil {
		return err
	}

	// Update service.
	cfg, err = ioutil.ReadFile(repoPath("cloud/kube/service/" + server + ".yaml"))
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

func (c *Config) kubeCredentials() error {
	log.Print("Fetching kubectl credentials")

	cmd := exec.Command("gcloud", "--project", c.Project, "container", "clusters", "--zone", c.Zone, "get-credentials", c.clusterName())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) buildBaseImage() error {
	log.Println("Building base Docker image")
	return cdbuild(repoPath("cloud/docker/base"), c.Project, "base")
}

func (c *Config) dirServerUserName() string {
	if c.inProd() {
		// HACK: see the comment on inProd.
		return "upspin-dir@upspin.io"
	}
	return "upspin-dir@" + c.Domain
}

func writeRC(dir string, lines ...string) error {
	var buf bytes.Buffer
	for _, s := range lines {
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

func (c *Config) installDeps() error {
	log.Print("Running 'go install' for dependencies")

	// Find all dependencies of the servers we are going to build.
	args := []string{"list", "-f", `{{join .Imports "\n"}}`}
	for _, s := range c.servers() {
		args = append(args, "upspin.io/cmd/"+s)
	}
	cmd := exec.Command("go", args...)
	cmd.Env = c.buildEnv()
	cmd.Stderr = os.Stderr
	depBytes, err := cmd.Output()
	if err != nil {
		return err
	}
	deps := strings.Split(string(bytes.TrimSpace(depBytes)), "\n")

	// Build them for GOOS=linux GOARCH=amd64.
	cmd = exec.Command("go", append([]string{"install"}, deps...)...)
	cmd.Env = c.buildEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) buildBinary(dir, server string) error {
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

func kubeApply(cfg []byte) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) restartServer(server string) error {
	log.Printf("Restarting %q", server)

	cmd := exec.Command("kubectl", "delete", "pods", "-l", "app="+c.Prefix+server)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Config) endpoint(server string) string {
	if server == "keyserver" && !c.KeyServer {
		return defaultKeyServerEndpoint
	}
	return "remote," + c.hostName(server) + ":443"
}

func (c *Config) hostName(server string) string {
	var host string
	switch server {
	case "keyserver":
		host = "key"
	case "storeserver":
		host = "store"
	case "dirserver":
		host = "dir"
	case "frontend":
		return c.Domain
	default:
		panic("unknown server: " + server)
	}
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
	return okReason("alreadyExists", c.waitOp(svc, op, err))
}

func (c *Config) deleteNetwork() error {
	log.Printf("Deleting network %q", c.networkName())

	svc, err := computeService()
	if err != nil {
		return err
	}

	// Delete associated firewalls.
	ctx := context.Background()
	err = svc.Firewalls.List(c.Project).Filter("network eq .*/"+c.networkName()+"$").Pages(ctx, func(list *compute.FirewallList) error {
		for _, fw := range list.Items {
			op, err := svc.Firewalls.Delete(c.Project, fw.Name).Do()
			err = okReason("notFound", c.waitOp(svc, op, err))
			if err != nil {
				return err
			}
		}
		return nil
	})
	err = okReason("notFound", err)
	if err != nil {
		return err
	}

	// Delete associated routes.
	err = svc.Routes.List(c.Project).Filter("network eq .*/"+c.networkName()+"$").Pages(ctx, func(list *compute.RouteList) error {
		for _, r := range list.Items {
			if strings.HasPrefix(r.Name, "default-route-") {
				continue
			}
			op, err := svc.Routes.Delete(c.Project, r.Name).Do()
			err = okReason("notFound", c.waitOp(svc, op, err))
			if err != nil {
				return err
			}
		}
		return nil
	})
	err = okReason("notFound", err)
	if err != nil {
		return err
	}

	op, err := svc.Networks.Delete(c.Project, c.networkName()).Do()
	return okReason("notFound", c.waitOp(svc, op, err))
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
					compute.ComputeScope,
					// Required to read/write to cloud storage.
					storage.ScopeReadWrite,
					// Required to write metrics.
					cloudtrace.TraceAppendScope,
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
	return okReason("alreadyExists", err)
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
	return okReason("notFound", err)
}

var bucketSuffixes = []string{
	"cdbuild",
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
		if err := okReason("conflict", err); err != nil {
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

		// Delete bucket contents.
		it := client.Bucket(name).Objects(ctx, nil)
		for {
			o, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return okReason("notFound", err)
			}
			err = okReason("notFound", client.Bucket(name).Object(o.Name).Delete(ctx))
			if err != nil && err != storage.ErrObjectNotExist {
				return err
			}
		}

		err := client.Bucket(name).Delete(ctx)
		if err := okReason("notFound", err); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) addressName(suffix string) string {
	if c.inProd() {
		// HACK: see the comment on inProd.
		switch suffix {
		case "dirserver":
			return "directory"
		case "storeserver":
			return "store"
		case "keyserver":
			return "user"
		}
	}
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
		if err := okReason("alreadyExists", c.waitOp(svc, op, err)); err != nil {
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
		if err := okReason("notFound", c.waitOp(svc, op, err)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) zoneName() string {
	return c.Prefix + "zone"
}

func (c *Config) createZone() error {
	name := c.zoneName()
	log.Printf("Creating zone %q", name)

	svc, err := dnsService()
	if err != nil {
		return err
	}

	zone := &dns.ManagedZone{
		DnsName:     c.Domain + ".",
		Name:        name,
		Description: "upspin cluster",
	}
	_, err = svc.ManagedZones.Create(c.Project, zone).Do()
	err = okReason("alreadyExists", err)
	if err != nil {
		return err
	}

	var records []*dns.ResourceRecordSet
	for _, s := range c.servers() {
		ip, err := c.ipAddress(s)
		if err != nil {
			return err
		}
		records = append(records, &dns.ResourceRecordSet{
			Name:    c.hostName(s) + ".",
			Type:    "A",
			Rrdatas: []string{ip},
		})
	}

	change := &dns.Change{Additions: records}
	change, err = svc.Changes.Create(c.Project, name, change).Do()
	for err == nil && change.Status == "pending" {
		time.Sleep(1 * time.Second)
		change, err = svc.Changes.Get(c.Project, name, change.Id).Do()
	}
	return okReason("alreadyExists", err)
}

func (c *Config) deleteZone() error {
	name := c.zoneName()
	log.Printf("Deleting zone %q", name)

	svc, err := dnsService()
	if err != nil {
		return err
	}

	// List and delete records first.
	var records []*dns.ResourceRecordSet
	ctx := context.Background()
	err = svc.ResourceRecordSets.List(c.Project, name).Pages(ctx, func(resp *dns.ResourceRecordSetsListResponse) error {
		for _, r := range resp.Rrsets {
			if r.Type != "A" {
				continue
			}
			records = append(records, r)
		}
		return nil
	})
	err = okReason("notFound", err)
	if err != nil {
		return err
	}
	if len(records) > 0 {
		change := &dns.Change{Deletions: records}
		change, err = svc.Changes.Create(c.Project, name, change).Do()
		for err == nil && change.Status == "pending" {
			time.Sleep(1 * time.Second)
			change, err = svc.Changes.Get(c.Project, name, change.Id).Do()
		}
		if err = okReason("notFound", err); err != nil {
			return err
		}
	}

	return okReason("notFound", svc.ManagedZones.Delete(c.Project, name).Do())
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
	return okReason("alreadyExists", c.waitOp(svc, op, err))
}

func (c *Config) deleteDisks() error {
	name := c.diskName("dirserver")
	log.Printf("Deleting disk %q", name)

	svc, err := computeService()
	if err != nil {
		return err
	}

	op, err := svc.Disks.Delete(c.Project, c.Zone, name).Do()
	return okReason("notFound", c.waitOp(svc, op, err))
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

func dnsService() (*dns.Service, error) {
	client, err := google.DefaultClient(context.Background(), compute.ComputeScope)
	if err != nil {
		return nil, err
	}
	return dns.New(client)
}

func (c *Config) waitOp(svc *compute.Service, op *compute.Operation, err error) error {
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(1 * time.Second)
		switch {
		case op.Zone != "":
			op, err = svc.ZoneOperations.Get(c.Project, c.Zone, op.Name).Do()
		case op.Region != "":
			op, err = svc.RegionOperations.Get(c.Project, c.Region, op.Name).Do()
		default:
			op, err = svc.GlobalOperations.Get(c.Project, op.Name).Do()
		}
	}
	return opError(op, err)
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

func wrap(s string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %v", s, err)
}

func okReason(reason string, err error) error {
	if e, ok := err.(*googleapi.Error); ok && len(e.Errors) > 0 {
		ok := true
		for _, e := range e.Errors {
			if e.Reason != reason {
				ok = false
			}
		}
		if ok {
			return nil
		}
	}
	return err
}

// TODO(adg): move to osutil
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
