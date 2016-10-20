// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command upbox builds and runs inprocess key, store, and directory servers
// and provides an upspin shell acting as the test user bob@b.com.
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
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

	"upspin.io/upspin"

	"gopkg.in/yaml.v2"
)

var (
	logLevel = flag.String("log", "info", "log `level`")
	basePort = flag.Int("port", 8000, "base `port` number for upspin servers")
	config   = flag.String("config", "", "configuration `file` name")
)

func main() {
	flag.Parse()

	cfg, err := readConfig(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := cfg.Do(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const defaultConfig = `
domain: example.com
users:
  - name: user
servers:
  - name: keyserver
  - name: storeserver
  - name: dirserver
`

type Config struct {
	Domain  string
	Users   []*User
	Servers []*Server

	KeyServer string // optional; default keyserver

	user   map[string]*User
	server map[string]*Server
}

type User struct {
	Name string // required

	StoreServer string // optional; default is storeserver. may be "$servername".
	DirServer   string // optional; default is dirserver. may be "$servername".
	Packing     string // optional; default is plain.

	secrets string // set by Do
}

type Server struct {
	Name       string // required
	User       string // optional; default is a user of this name
	ImportPath string // optional; default is "upspin.io/cmd/Name"

	addr string // set by readConfig
}

func readConfig(name string) (*Config, error) {
	var data []byte
	if name == "" {
		data = []byte(defaultConfig)
	} else {
		var err error
		data, err = ioutil.ReadFile(name)
		if err != nil {
			return nil, err
		}
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.Domain == "" {
		return nil, errors.New("domain must be specified")
	}

	cfg.user = map[string]*User{}
	cfg.server = map[string]*Server{}

	// Add domain to usernames without domains,
	// and default user names for servers.
	for _, u := range cfg.Users {
		if u.Name == "" {
			return nil, errors.New("user name must be specified")
		}

		// Add domain to bare user name.
		if !strings.Contains(u.Name, "@") {
			u.Name += "@" + cfg.Domain
		}
		if u.Packing == "" {
			u.Packing = "plain"
		}

		cfg.user[u.Name] = u
	}

	port := *basePort
	for _, s := range cfg.Servers {
		if s.Name == "" {
			return nil, errors.New("server name must be specified")
		}

		cfg.server[s.Name] = s

		if s.User == "" {
			// If there's no user, create one.
			s.User = s.Name + "@" + cfg.Domain
			if _, ok := cfg.user[s.User]; !ok {
				u := &User{Name: s.User}
				switch s.Name {
				case "keyserver":
					u.Packing = "plain"
					u.StoreServer = "unassigned,"
					u.DirServer = "unassigned,"
				case "storeserver":
					u.Packing = "plain"
					u.StoreServer = "inprocess,"
					u.DirServer = "unassigned,"
				default:
					u.Packing = "ee"
					// StoreServer and DirServer
					// will be set up later in this func.
				}

				cfg.Users = append(cfg.Users, u)
				cfg.user[u.Name] = u
			}
		} else if !strings.Contains(s.User, "@") {
			// Add the domain name if user is specified.
			s.User += "@" + cfg.Domain
		}

		// Pick address for this service.
		s.addr = fmt.Sprintf("localhost:%d", port)
		port++

		// Set the global keyserver, if none provided.
		if s.Name == "keyserver" && cfg.KeyServer == "" {
			cfg.KeyServer = s.addr
		}

		// Default to an Upspin command if no import path provided.
		if s.ImportPath == "" {
			s.ImportPath = "upspin.io/cmd/" + s.Name
		}
	}

	if cfg.KeyServer == "" {
		return nil, errors.New("no keyserver in configuration")
	}

	// Set or evaluate DirServer and StoreServer fields.
	for _, u := range cfg.Users {
		if u.DirServer == "" {
			s, ok := cfg.server["dirserver"]
			if !ok {
				return nil, fmt.Errorf("user %q needs default dirserver, but none found", u.Name)
			}
			u.DirServer = "remote," + s.addr
		} else if u.DirServer[0] == '$' {
			name := u.DirServer[1:]
			s, ok := cfg.server[name]
			if !ok {
				return nil, fmt.Errorf("user %q needs dirserver %q, but none found", u.Name, name)
			}
			u.DirServer = "remote," + s.addr
		}

		if u.StoreServer == "" {
			s, ok := cfg.server["storeserver"]
			if !ok {
				return nil, fmt.Errorf("user %q needs default storeserver, but none found", u.Name)
			}
			u.StoreServer = "remote," + s.addr
		} else if u.StoreServer[0] == '$' {
			name := u.StoreServer[1:]
			s, ok := cfg.server[name]
			if !ok {
				return nil, fmt.Errorf("user %q needs storeserver %q, but none found", u.Name, name)
			}
			u.StoreServer = "remote," + s.addr
		}
	}

	return &cfg, nil
}

func (cfg *Config) Do() error {
	// Start upspin shell.
	if len(cfg.Users) == 0 {
		return fmt.Errorf("zero users; can't do anything")
	}

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
	for _, u := range cfg.Users {
		fmt.Printf("Generating keys for user %q\n", u.Name)
		dir := userDir(u.Name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		keygen := exec.Command("upspin", "keygen", "-where="+dir)
		keygen.Stdout = prefix("keygen: ", os.Stdout)
		keygen.Stderr = prefix("keygen: ", os.Stderr)
		if err := keygen.Run(); err != nil {
			return err
		}
		u.secrets = dir
	}

	writeRC := func(server, user string) (string, error) {
		u, ok := cfg.user[user]
		if !ok {
			return "", fmt.Errorf("unknown user %q", user)
		}

		rcContent := []string{
			"username=" + u.Name,
			"tlscerts=" + tmpDir,
			"packing=" + u.Packing,
			"storeserver=" + u.StoreServer,
			"dirserver=" + u.DirServer,
		}
		switch server {
		case "keyserver":
			rcContent = append(rcContent,
				"keyserver=inprocess,",
				"secrets=none",
			)
		default:
			rcContent = append(rcContent,
				"keyserver=remote,"+cfg.KeyServer,
				"secrets="+userDir(user),
			)
		}
		rcFile := filepath.Join(tmpDir, "rc."+server)
		if err := ioutil.WriteFile(rcFile, []byte(strings.Join(rcContent, "\n")), 0644); err != nil {
			return "", err
		}
		return rcFile, nil
	}

	// Start servers.
	for i := range cfg.Servers {
		s := cfg.Servers[i]

		rcFile, err := writeRC(s.Name, s.User)
		if err != nil {
			return fmt.Errorf("writing rc for %v: %v", s.Name, err)
		}

		args := []string{
			"-context=" + rcFile,
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
	rcFile, err := writeRC("key-bootstrap", keyUser)
	if err != nil {
		return err
	}
	for _, u := range cfg.Users {
		pk, err := ioutil.ReadFile(filepath.Join(userDir(u.Name), "public.upspinkey"))
		if err != nil {
			return err
		}
		user := &upspin.User{
			Name:      upspin.UserName(u.Name),
			Dirs:      []upspin.Endpoint{{upspin.Remote, upspin.NetAddr(u.DirServer)}},
			Stores:    []upspin.Endpoint{{upspin.Remote, upspin.NetAddr(u.StoreServer)}},
			PublicKey: upspin.PublicKey(pk),
		}
		userJSON, err := json.Marshal(user)
		if err != nil {
			return err
		}
		cmd := exec.Command("upspin",
			"-context="+rcFile,
			"-log="+*logLevel,
			"user", "-put",
		)
		cmd.Stdin = bytes.NewReader(userJSON)
		cmd.Stdout = prefix("key-bootstrap:\t", os.Stdout)
		cmd.Stderr = prefix("key-bootstrap:\t", os.Stderr)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Start a shell as the first user.
	rcFile, err = writeRC("shell", cfg.Users[0].Name)
	if err != nil {
		return err
	}
	shell := exec.Command("upspin",
		"-context="+rcFile,
		"-log="+*logLevel,
		"shell",
	)
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
