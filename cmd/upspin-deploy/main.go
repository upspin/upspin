// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upspin-deploy creates and deploys an Upspin cluster
// on the Google Cloud Platform.
package main

// TODO(adg): comprehensive help/setup text
// TODO(adg): delete load balancers
// TODO(adg): kubectl delete services
// TODO(adg): delete container registry entries
// TODO(adg): only create base image once, check if it exists

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

	"upspin.io/config"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2/google"
	cloudtrace "google.golang.org/api/cloudtrace/v1"
	compute "google.golang.org/api/compute/v1"
	container "google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// The user must first:
// - Create a Google Cloud Project: https://cloud.google.com/iam-admin/projects
//   - Make sure it has an associated Billing Account.
// - Authenticate using the gcloud tool:
//   $ gcloud auth login
//   $ gcloud auth application-default login

// requiredAPIs lists the Google Cloud APIs required by an Upspin installation.
var requiredAPIs = []string{
	"cloudbuild.googleapis.com",
	"cloudtrace.googleapis.com",
	"compute_component",
	"container",
	"dns.googleapis.com",
	"logging.googleapis.com",
	"storage_api",
}

var (
	project = flag.String("project", "", "Google Cloud Project `ID`")
	zone    = flag.String("zone", "us-central1-a", "Google Cloud `Zone`")

	prefix = flag.String("prefix", "", "A `string` that begins all resource names")
	domain = flag.String("domain", "", "The base domain `name` for the Upspin services")

	machineType = flag.String("machinetype", "g1-small", "GCP machine type for servers")

	keyserver = flag.String("keyserver", defaultKeyServer, "Key server `host:port` (empty means use this cluster's KeyServer)")

	create = flag.Bool("create", false, "Create cloud services")
	deploy = flag.Bool("deploy", true, "Deploy Upspin servers")
	delete = flag.String("delete", "", "Delete cloud services (string must equal the `project` name, for safety)")

	all = flag.Bool("all", false, "Create/deploy/delete all servers")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %v [flags] [servers...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "The specified servers are those to be created, deployed, or deleted.\n")
		fmt.Fprintf(os.Stderr, "They may be any combination of\n")
		fmt.Fprintf(os.Stderr, "\t%s\n", strings.Join(validServers, " "))
		fmt.Fprintf(os.Stderr, "If no servers are provided, the default list is\n")
		fmt.Fprintf(os.Stderr, "\t%s\n\n", strings.Join(defaultServers, " "))
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
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

		MachineType: *machineType,

		Prefix: *prefix,
		Domain: *domain,

		KeyServer: *keyserver,

		Servers: flag.Args(),
	}

	if len(cfg.Servers) == 0 {
		cfg.Servers = defaultServers
	}
	if *all {
		cfg.Servers = validServers
	}

	if err := cfg.checkCredentials(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if *delete != "" {
		if *delete != *project {
			fmt.Fprintf(os.Stderr, "error: -delete must equal -project\n\n")
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
	if *create {
		cfg.PrintDNSAdvisory()
	}
}

func mustProvideFlag(f *string, name string) {
	if *f == "" {
		fmt.Fprintf(os.Stderr, "error: you must specify the %s flag\n\n", name)
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

	// Machine type is the type of machine to use for Store and Dir servers.
	// Example: "n1-standard-1", "f1-micro", "g1-small".
	MachineType string

	// Prefix is a string that is used as the prefix for all resource names
	// (buckets, disks, clusters, etc). It may be empty. By varying Prefix
	// one can run multiple Upspin clusters in a single cloud project.
	Prefix string

	// Domain specifies the IANA domain under which these services will
	// run. For example, if it is set to "example.com" then the storeserver
	// will be "store.example.com".
	Domain string

	// KeyServer address. If empty, uses KeyServer in this cluster.
	KeyServer string

	// Servers specifies the servers to deploy.
	// Valid values are "dirserver", "storeserver", "keyserver", and "frontend".
	Servers []string
}

var (
	validServers   = []string{"dirserver", "storeserver", "keyserver", "frontend"}
	defaultServers = []string{"dirserver", "storeserver"}
)

func (c *Config) servers() (ss []string) {
	for _, s := range c.Servers {
		for _, s2 := range validServers {
			if s == s2 {
				ss = append(ss, s)
			}
		}
	}
	return
}

const defaultKeyServer = "key.upspin.io:443"

func (c *Config) inProd() bool {
	// The upspin-prod and upspin-test projects were created
	// before this script was written, and so they use some
	// different names for things. We hard code those special
	// things here to avoid re-creating everything.
	return (c.Project == "upspin-prod" || c.Project == "upspin-test") && c.Prefix == ""
}

func (c *Config) Create() error {
	log.Printf("Creating cloud services: Project=%q Zone=%q Prefix=%q", c.Project, c.Zone, c.Prefix)

	if err := wrap("enableAPIs", c.enableAPIs()); err != nil {
		return err
	}

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
		if err := wrap("buckets", c.createBuckets()); err != nil {
			errc <- err
			return
		}
		// Base image depends on storage buckets.
		errc <- wrap("base", c.buildBaseImage())
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

	return nil
}

func (c *Config) Delete() error {
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
	log.Printf("Deploying Upspin servers %v: Project=%q Zone=%q Prefix=%q", c.servers(), c.Project, c.Zone, c.Prefix)

	// Install dependencies to speed builds.
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

func (c *Config) checkCredentials() error {
	cmd := exec.Command("gcloud", "auth", "application-default", "print-access-token")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(`Default credentials not available; try "gcloud auth application-default login".`)
	}

	cmd = exec.Command("gcloud", "projects", "describe", c.Project)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(`Credentials not available; try "gcloud auth login".`)
	}

	return nil
}

func (c *Config) enableAPIs() error {
	// Make sure the "beta" gcloud commands are available.
	// TODO(adg): when they move out of beta, remove this.
	cmd := exec.Command("gcloud", "components", "install", "beta")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("installing gcloud beta components: %v\n%s", err, out)
	}

	// See which APIs are enabled already.
	var out bytes.Buffer
	cmd = exec.Command("gcloud", "--project", c.Project, "beta", "service-management", "list")
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return wrap("list services", err)
	}

	enabled := map[string]bool{}
	for i, s := range strings.Split(string(bytes.TrimSpace(out.Bytes())), "\n") {
		if i == 0 {
			// Skip column headings.
			continue
		}
		api := strings.Fields(s)[0]
		enabled[api] = true
	}

	for _, api := range requiredAPIs {
		if enabled[api] {
			continue
		}
		log.Printf("Enabling API %q", api)
		cmd := exec.Command("gcloud", "--project", c.Project, "beta", "service-management", "enable", api)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("enabling API %q: %v\n%s", api, err, out)
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
		err = writeConfig(dir,
			"username: "+c.dirServerUserName(),
			"secrets: /upspin",
			"keyserver: "+c.endpoint("keyserver"),
			"storeserver: "+c.endpoint("storeserver"),
			"dirserver: "+c.endpoint("dirserver"),
		)
	case "storeserver":
		err = writeConfig(dir,
			"username: "+c.storeServerUserName(),
			"secrets: /upspin",
			"keyserver: "+c.endpoint("keyserver"),
			"storeserver: "+c.endpoint("storeserver"), // So that it knows who it is.
			"dirserver: "+c.endpoint("dirserver"),
		)
	case "keyserver":
		err = writeConfig(dir,
			"username: "+c.keyServerUserName(),
			"secrets: /upspin",
			"keyserver: "+c.endpoint("keyserver"), // So that it knows who it is.
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
		}
	case "keyserver":
		files = []string{
			"mailconfig",
			"public.upspinkey",
			"secret.upspinkey",
		}
	case "storeserver":
		files = []string{
			"public.upspinkey",
			"secret.upspinkey",
		}
	}
	home, err := config.Homedir()
	if err != nil {
		return err
	}
	base := filepath.Join(home, "upspin", "deploy", c.Domain, server)
	for _, f := range files {
		if err := cp(filepath.Join(dir, f), filepath.Join(base, f)); err != nil {
			return fmt.Errorf("error copying %q for %v: %v", f, server, err)
		}
	}

	if err := c.copyDockerfile(dir, server); err != nil {
		return err
	}

	// Collect source code for server and its dependencies.
	pkgPath := "upspin.io/cmd/" + server
	if err := c.copySource(dir, pkgPath); err != nil {
		return err
	}

	name := c.Prefix + server
	log.Printf("Building Docker image %q", name)
	if err := cdbuild(dir, c.Project, name, pkgPath); err != nil {
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
	log.Println("Building cloudbuild Docker image")
	return cdbuild(repoPath("cloud/docker/cloudbuild"), c.Project, "cloudbuild", "")
}

func (c *Config) dirServerUserName() string {
	if c.inProd() {
		// HACK: see the comment on inProd.
		return "upspin-dir@upspin.io"
	}
	return "upspin-dir@" + c.Domain
}

func (c *Config) storeServerUserName() string {
	if c.inProd() {
		// HACK: see the comment on inProd.
		return "upspin-store@upspin.io"
	}
	return "upspin-store@" + c.Domain
}

func (c *Config) keyServerUserName() string {
	if c.inProd() {
		// HACK: see the comment on inProd.
		return "upspin-key@upspin.io"
	}
	return "upspin-key@" + c.Domain
}

func writeConfig(dir string, lines ...string) error {
	var buf bytes.Buffer
	for _, s := range lines {
		buf.WriteString(s)
		buf.WriteByte('\n')
	}
	return ioutil.WriteFile(filepath.Join(dir, "config"), buf.Bytes(), 0644)
}

func (c *Config) copyDockerfile(dir, server string) error {
	data, err := ioutil.ReadFile(repoPath("cloud/docker/" + server + "/Dockerfile"))
	if err != nil {
		return err
	}
	data = c.prepareConfig(data, server)
	return ioutil.WriteFile(filepath.Join(dir, "Dockerfile"), data, 0644)
}

func (c *Config) logLevel() string {
	switch {
	case strings.Contains(c.Project, "test"),
		strings.Contains(c.Project, "dev"),
		// TODO: remove when done debugging upspin-prod.
		c.Project == "upspin-prod":
		return "debug"
	}
	return "info"
}

func (c *Config) prepareConfig(data []byte, server string) []byte {
	data = bytes.Replace(data, []byte("PREFIX"), []byte(c.Prefix), -1)
	data = bytes.Replace(data, []byte("PROJECT"), []byte(c.Project), -1)
	data = bytes.Replace(data, []byte("STORESERVERUSER"), []byte(c.storeServerUserName()), -1)
	data = bytes.Replace(data, []byte("LOGLEVEL"), []byte(c.logLevel()), -1)

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

// copySource copies the source code for the specified package and all
// its non-goroot dependencies to the specified workspace directory.
func (c *Config) copySource(dir, pkgPath string) error {
	// Find all package dependencies.
	cmd := exec.Command("go", "list", "-f", `{{join .Deps "\n"}}`, pkgPath)
	cmd.Stderr = os.Stderr
	cmd.Env = c.buildEnv()
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	// Find the directories for the non-goroot packages.
	deps := strings.Split(string(bytes.TrimSpace(out)), "\n")
	args := []string{"list", "-f", `{{if not .Goroot}}{{.ImportPath}} {{.Dir}}{{end}}`, pkgPath}
	cmd = exec.Command("go", append(args, deps...)...)
	cmd.Stderr = os.Stderr
	cmd.Env = c.buildEnv()
	out, err = cmd.Output()
	if err != nil {
		return err
	}

	if pkgPath == "upspin.io/cmd/frontend" {
		gopath := os.Getenv("GOPATH")

		// Copy frontend dependencies not listed by `go list`.
		for _, d := range []string{
			"doc",
			"doc/images",
			"doc/templates",
		} {
			s := fmt.Sprintf("%s %s\n",
				filepath.Join("upspin.io", d),
				filepath.Join(gopath, "src/upspin.io", d))
			out = append(out, s...)
		}
	}

	// Copy the contents of those directories to a workspace at dir.
	for _, line := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
		pair := strings.SplitN(line, " ", 2)
		if len(pair) != 2 {
			return fmt.Errorf("unexpected 'go list' output: %q", line)
		}
		pkgPath, pkgDir := pair[0], pair[1]
		dstDir := filepath.Join(dir, "src", pkgPath)
		if err := cpDir(dstDir, pkgDir); err != nil {
			return fmt.Errorf("copying %q: %v", pkgPath, err)
		}
	}

	return nil
}

func (c *Config) buildEnv() (env []string) {
	for _, s := range os.Environ() {
		switch {
		case strings.HasPrefix(s, "GOOS="),
			strings.HasPrefix(s, "GOARCH="),
			strings.HasPrefix(s, "CGO_ENABLED="):
			// Skip.
		default:
			env = append(env, s)
		}
	}
	return append(env, "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
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
	if server == "keyserver" && c.KeyServer != "" {
		return "remote," + c.KeyServer
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
	err = okReason("alreadyExists", c.waitOp(svc, op, err))
	if err != nil {
		return err
	}
	if op == nil {
		// op is nil when the network already exists.
		// Assume firewall has been created too.
		return nil
	}

	// Setup SSH firewall rule.
	ssh := &compute.Firewall{
		Name:         "ssh",
		Network:      op.TargetLink,
		SourceRanges: []string{"0.0.0.0/0"},
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{"22"},
			},
		},
	}
	op, err = svc.Firewalls.Insert(c.Project, ssh).Do()
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
				MachineType: c.MachineType,
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
		if c.KeyServer != "" && s == "key" {
			// If we're not using our own KeyServer,
			// don't create a bucket for it.
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
		if c.KeyServer != "" && s == "key" {
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
	name := suffix
	switch suffix {
	case "dirserver":
		name = "dir"
	case "storeserver":
		name = "store"
	case "keyserver":
		name = "key"
	}
	return c.Prefix + name
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

func (c *Config) PrintDNSAdvisory() error {
	addrs := make(map[string]string) // [host]ip
	for _, s := range c.servers() {
		host := c.hostName(s)
		ip, err := c.ipAddress(s)
		if err != nil {
			return err
		}
		addrs[host] = ip
	}
	fmt.Print("\n==== User action required ===\n\n")
	fmt.Println("You must configure your domain name servers to describe the")
	fmt.Println("new servers by adding A records for these hosts and IP addresses:")
	for host, ip := range addrs {
		fmt.Printf("\t%s\t%s\n", ip, host)
	}
	fmt.Println()
	return nil
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
		for _, e := range e.Errors {
			if e.Reason != reason {
				return err
			}
		}
		return nil
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

// cpDir copies the contents of the source directory to the destination
// directory, making it if necessary. It does not copy sub-directories.
func cpDir(dst, src string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	walk := func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if srcPath == src {
				// Don't skip the directory we're copying.
				return nil
			}
			return filepath.SkipDir
		}
		dstPath := filepath.Join(dst, srcPath[len(src):])
		return cp(dstPath, srcPath)
	}
	return filepath.Walk(src, walk)
}

func repoPath(suffix string) string {
	return filepath.Join(os.Getenv("GOPATH"), "src/upspin.io", suffix)
}
