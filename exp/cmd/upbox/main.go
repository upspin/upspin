// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Command upbox builds and runs Upspin servers as specified by a configuration
file and provides an upspin shell acting as the first user specified by the
configuration.

Configuration files must be in YAML format, of this general form:

	users:
	- name: joe
	- name: jess@example.net
	  storeserver: store.upspin.io
	  dirserver: dir.upspin.io
	  packing: ee
	servers:
	- name: storeserver
	- name: dirserver
	  user: joe
	- name: myserver
	  importpath: github.com/user/myserver
	keyserver: key.uspin.io
	domain: exmaple.com


The Users and Servers lists specify the users and servers to create within this
configuration.

Users

Name specifies the user name of this user.
It must be non-empty.
It can be a full email address, or just the user component.
In the latter case, the top-level domain field must be set.

StoreServer and DirServer specify the store and directory endpoints for this
user. If empty, they default to the servers "storeserver" and "dirserver",
respectively. If they are of the form "$servername" then the address of the
server "servername" is used.

Packing specifies the packing method for this user.
If empty, it defaults to "ee".

Servers

Name specifies a short name for this server. It must be non-empty.
The names "keyserver", "storeserver", and "dirserver" represent useful
defaults.

User specifies the user to run this server as.
It can be a full email address, or just the user component.
If empty, the Name of the server is combined with the
Config's Domain and a user is created with that name.
In the latter cases, the top-level Domain field must be set.

ImportPath specifies the import path for this server that is built before
starting the server. If empty, the server Name is appended to the string
"upspin.io/cmd/".

Other top-level fields

KeyServer specifies the KeyServer that each user in the cluster
should use. If it is empty, then a Server named "keyserver" must
be included in the list of Servers, and the address of that server
is used.

Domain specifies a domain that is appended to any user names that do
not include a domain component.
Domain must be specified if any domain suffixes are omitted from
User Names or if a Servers is specified with an empty User field.

Default configuration

If no config is specified, the default configuration is used:

	users:
	  - name: user
	servers:
	  - name: keyserver
	  - name: storeserver
	  - name: dirserver
	domain: example.com

This creates the users user@example.com, keyserver@example.com,
storeserver@example.com, and dirserver@example.com, builds and runs
the servers keyserver, storeserver, and dirserver (running as their
respective users), and runs "upspin shell" as user@example.com.

*/
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v2"

	"upspin.io/upspin"
)

var (
	logLevel = flag.String("log", "info", "log `level`")
	basePort = flag.Int("port", 8000, "base `port` number for upspin servers")
	config   = flag.String("config", "", "configuration `file` name")
)

func main() {
	flag.Parse()

	cfg, err := ConfigFromFile(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upbox: error parsing config:", err)
		os.Exit(1)
	}

	if err := cfg.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "upbox:", err)
		os.Exit(1)
	}
}

func (cfg *Config) Run() error {
	// Build servers and commands.
	args := []string{"install", "upspin.io/cmd/upspin"}
	for _, s := range cfg.Servers {
		args = append(args, s.ImportPath)
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout = prefix("build: ", os.Stdout)
	cmd.Stderr = prefix("build: ", os.Stderr)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build error: %v", err)
	}

	// Create temporary directory.
	tmpDir, err := ioutil.TempDir("", "upbox")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	userDir := func(user string) string { return filepath.Join(tmpDir, user) }

	// Generate TLS certificates.
	if err := generateCert(tmpDir); err != nil {
		return err
	}

	// Generate keys.
	// Write an empty  file for use by 'upspin keygen'.
	configKeygen := filepath.Join(tmpDir, "config.keygen")
	if err := ioutil.WriteFile(configKeygen, []byte("secrets: none"), 0644); err != nil {
		return err
	}
	for _, u := range cfg.Users {
		fmt.Fprintf(os.Stderr, "upbox: generating keys for user %q\n", u.Name)
		dir := userDir(u.Name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		keygen := exec.Command("upspin", "-config="+configKeygen, "keygen", "-where="+dir)
		keygen.Stdout = prefix("keygen: ", os.Stdout)
		keygen.Stderr = prefix("keygen: ", os.Stderr)
		if err := keygen.Run(); err != nil {
			return err
		}
		u.secrets = dir
	}

	writeConfig := func(server, user string) (string, error) {
		u, ok := cfg.user[user]
		if !ok {
			return "", fmt.Errorf("unknown user %q", user)
		}

		configContent := []string{
			"username: " + u.Name,
			"secrets: " + userDir(user),
			"tlscerts: " + tmpDir,
			"packing: " + u.Packing,
			"storeserver: " + u.StoreServer,
			"dirserver: " + u.DirServer,
		}
		switch server {
		case "keyserver":
			configContent = append(configContent,
				"keyserver: inprocess,",
			)
		default:
			configContent = append(configContent,
				"keyserver: remote,"+cfg.KeyServer,
			)
		}
		configFile := filepath.Join(tmpDir, "config."+server)
		if err := ioutil.WriteFile(configFile, []byte(strings.Join(configContent, "\n")), 0644); err != nil {
			return "", err
		}
		return configFile, nil
	}

	// Start servers.
	for i := range cfg.Servers {
		s := cfg.Servers[i]

		configFile, err := writeConfig(s.Name, s.User)
		if err != nil {
			return fmt.Errorf("writing config for %v: %v", s.Name, err)
		}

		args := []string{
			"-config=" + configFile,
			"-log=" + *logLevel,
			"-tls_cert=" + filepath.Join(tmpDir, "cert.pem"),
			"-tls_key=" + filepath.Join(tmpDir, "key.pem"),
			"-https=" + s.addr,
			"-addr=" + s.addr,
		}
		if s.Name == "keyserver" {
			args = append(args,
				"-test_user="+s.User,
				"-test_secrets="+userDir(s.User),
			)
		}
		cmd := exec.Command(s.Name, args...)
		cmd.Stdout = prefix(s.Name+":\t", os.Stdout)
		cmd.Stderr = prefix(s.Name+":\t", os.Stderr)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting %v: %v", s.Name, err)
		}
		defer kill(cmd)
	}

	// Wait for the keyserver to start and add the users to it.
	if err := waitReady(cfg.KeyServer); err != nil {
		return err
	}
	keyUser := cfg.Users[0].Name
	if s, ok := cfg.server["keyserver"]; ok {
		keyUser = s.User
	}
	configFile, err := writeConfig("key-bootstrap", keyUser)
	if err != nil {
		return err
	}
	for _, u := range cfg.Users {
		pk, err := ioutil.ReadFile(filepath.Join(userDir(u.Name), "public.upspinkey"))
		if err != nil {
			return err
		}
		dir, err := upspin.ParseEndpoint(u.DirServer)
		if err != nil {
			return err
		}
		store, err := upspin.ParseEndpoint(u.StoreServer)
		if err != nil {
			return err
		}
		user := &upspin.User{
			Name:      upspin.UserName(u.Name),
			Dirs:      []upspin.Endpoint{*dir},
			Stores:    []upspin.Endpoint{*store},
			PublicKey: upspin.PublicKey(pk),
		}
		userYAML, err := yaml.Marshal(user)
		if err != nil {
			return err
		}
		cmd := exec.Command("upspin",
			"-config="+configFile,
			"-log="+*logLevel,
			"user", "-put",
		)
		cmd.Stdin = bytes.NewReader(userYAML)
		cmd.Stdout = prefix("key-bootstrap:\t", os.Stdout)
		cmd.Stderr = prefix("key-bootstrap:\t", os.Stderr)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Start a shell as the first user.
	configFile, err = writeConfig("shell", cfg.Users[0].Name)
	if err != nil {
		return err
	}
	args = []string{
		"-config=" + configFile,
		"-log=" + *logLevel,
		"shell",
	}
	fmt.Fprintf(os.Stderr, "upbox: upspin %s\n", strings.Join(args, " "))
	shell := exec.Command("upspin", args...)
	shell.Stdin = os.Stdin
	shell.Stdout = os.Stdout
	shell.Stderr = os.Stderr
	return shell.Run()
}

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

func prefix(p string, out io.Writer) io.Writer {
	r, w := io.Pipe()
	go func() {
		s := bufio.NewScanner(r)
		for s.Scan() {
			fmt.Fprintf(out, "%s%s\n", p, s.Bytes())
		}
	}()
	return w
}

func waitReady(addr string) error {
	rt := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	req, _ := http.NewRequest("GET", "https://"+addr, nil)
	for i := 0; i < 10; i++ {
		_, err := rt.RoundTrip(req)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		return nil
	}
	return fmt.Errorf("timed out waiting for %q to come up", addr)
}
