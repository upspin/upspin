// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package upbox provides the Schema mechanism for declaring and creating a set of
Upspin users and servers.

Schema files must be in YAML format, of this general form:

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
	  flags:
	    debug: cockroach
	keyserver: key.uspin.io
	domain: example.com


The Users and Servers lists specify the users and servers to create within this
schema.

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
Schema's Domain and a user is created with that name.
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

Default schema

If no schema is specified, the default schema is used:

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
package upbox // import "upspin.io/exp/upbox"

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
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

	yaml "gopkg.in/yaml.v2"
)

// Schema defines a set of Upspin Users and Servers.
type Schema struct {
	Users   []*User
	Servers []*Server

	// Domain specifies the default domain of any user names that do not
	// include a domain component.
	Domain string

	// KeyServer specifies the KeyServer used by each user in the cluster.
	KeyServer string

	user   map[string]*User
	server map[string]*Server
}

// User defines an Upspin user to be created and used within a schema.
type User struct {
	// Name specifies the user name of this user.
	Name string

	// StoreServer and DirServer specify the store and directory endpoints
	// for this user.
	StoreServer string
	DirServer   string

	// Packing specifies the packing method for this user.
	Packing string

	secrets string // path to user's public and private keys; set by Run
}

// Server defines an Upspin server to be created and used within a schema.
type Server struct {
	// Name specifies a short name for this server.
	Name string

	// User specifies the user to run this server as.
	User string

	// ImportPath specifies the import path for this server
	// that is built before starting the server.
	// If empty, the server Name is appended to the string
	// "upspin.io/cmd/".
	ImportPath string

	// Flags specifies command-line flags to supply to this server.
	Flags map[string]string

	addr string // the host:port of this server; set by readConfig
}

// DefaultSchema is the schema that is used if none is provided.
const DefaultSchema = `
users:
  - name: user
servers:
  - name: keyserver
  - name: storeserver
  - name: dirserver
domain: example.com
`

// SchemaFromFile parses a Schema from the named file.
// If no name is provided the DefaultSchema is used.
func SchemaFromFile(name string, basePort int) (*Schema, error) {
	var data []byte
	if name == "" {
		data = []byte(DefaultSchema)
	} else {
		var err error
		data, err = ioutil.ReadFile(name)
		if err != nil {
			return nil, err
		}
	}
	var sc Schema
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return nil, err
	}

	sc.user = map[string]*User{}
	sc.server = map[string]*Server{}

	if len(sc.Users) == 0 {
		return nil, errors.New("at least one user must be specified")
	}

	// Add domain to usernames without domains,
	// and default user names for servers.
	for i, u := range sc.Users {
		if u.Name == "" {
			return nil, fmt.Errorf("user[%d] must specify a name", i)
		}

		// Add domain to bare user name.
		if !strings.Contains(u.Name, "@") {
			if sc.Domain == "" {
				return nil, fmt.Errorf("user %q implies domain suffix, but domain not set", u.Name)
			}
			u.Name += "@" + sc.Domain
		}
		if u.Packing == "" {
			u.Packing = "ee"
		}

		// Add to map only after name has been normalized.
		sc.user[u.Name] = u
	}

	port := basePort
	for i, s := range sc.Servers {
		if s.Name == "" {
			return nil, fmt.Errorf("server[%d] must specify a name", i)
		}
		sc.server[s.Name] = s

		if s.User == "" {
			// If no user specified, default to server@domain.
			if sc.Domain == "" {
				return nil, fmt.Errorf("server %q specifies no user, but domain must be specified to create default user", s.Name)
			}
			s.User = s.Name + "@" + sc.Domain
			// If the user isn't otherwise provided, create it.
			if _, ok := sc.user[s.User]; !ok {
				u := newUserFor(s)
				sc.Users = append(sc.Users, u)
				sc.user[u.Name] = u
			}
		} else if !strings.Contains(s.User, "@") {
			// Add the domain name if user is specified.
			if sc.Domain == "" {
				return nil, fmt.Errorf("server %q specifies user %q without domain suffix, but domain not set", s.Name, s.User)
			}
			s.User += "@" + sc.Domain
		}

		// Pick address for this service.
		s.addr = fmt.Sprintf("localhost:%d", port)
		port++

		// Set the global keyserver, if none provided.
		if s.Name == "keyserver" && sc.KeyServer == "" {
			sc.KeyServer = s.addr
		}

		// Default to an Upspin command if no import path provided.
		if s.ImportPath == "" {
			s.ImportPath = "upspin.io/cmd/" + s.Name
		}
	}

	// Check for KeyServer only after we may have set it as "keyserver" above.
	if sc.KeyServer == "" {
		return nil, errors.New("no keyserver in configuration")
	}

	// Set or evaluate DirServer and StoreServer fields.
	for _, u := range sc.Users {
		if err := setServer(&sc, &u.DirServer, "dirserver"); err != nil {
			return nil, fmt.Errorf("user %q: %v", u.Name, err)
		}
		if err := setServer(&sc, &u.StoreServer, "storeserver"); err != nil {
			return nil, fmt.Errorf("user %q: %v", u.Name, err)
		}
	}

	return &sc, nil
}

func newUserFor(s *Server) *User {
	u := &User{Name: s.User}
	switch s.Name {
	case "keyserver":
		u.Packing = "ee"
		u.StoreServer = "unassigned,"
		u.DirServer = "unassigned,"
	case "storeserver":
		u.Packing = "ee"
		u.StoreServer = "inprocess,"
		u.DirServer = "unassigned,"
	default:
		u.Packing = "ee"
		// StoreServer and DirServer
		// will be set up later in this func.
	}
	return u
}

func setServer(sc *Schema, field *string, kind string) error {
	if *field == "" {
		s, ok := sc.server[kind]
		if !ok {
			return fmt.Errorf("needs default %s, but none found", kind)
		}
		*field = "remote," + s.addr
	} else if (*field)[0] == '$' {
		name := (*field)[1:]
		s, ok := sc.server[name]
		if !ok {
			return fmt.Errorf("specifies %v %q, but none found", kind, name)
		}
		*field = "remote," + s.addr
	}
	return nil
}

// Run sets up the Users and Servers specified by the Schema
// and runs an upspin shell as the first user in the Schema.
func (sc *Schema) Run(logLevel string) error {
	// Build servers and commands.
	args := []string{"install", "upspin.io/cmd/upspin"}
	for _, s := range sc.Servers {
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
	for _, u := range sc.Users {
		fmt.Fprintf(os.Stderr, "upbox: generating keys for user %q\n", u.Name)
		dir := filepath.Join(tmpDir, u.Name)
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

	keyUser := sc.Users[0].Name
	if s, ok := sc.server["keyserver"]; ok {
		keyUser = s.User
		// Start keyserver.
		cmd, err := sc.startServer(tmpDir, logLevel, s)
		if err != nil {
			return err
		}
		defer kill(cmd)
	}
	// Wait for the keyserver to start and add the users to it.
	if err := waitReady(sc.KeyServer); err != nil {
		return err
	}
	configFile, err := sc.writeConfig(tmpDir, "key-bootstrap", keyUser)
	if err != nil {
		return err
	}
	for _, u := range sc.Users {
		pk, err := ioutil.ReadFile(filepath.Join(tmpDir, u.Name, "public.upspinkey"))
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
			"-log="+logLevel,
			"user", "-put",
		)
		cmd.Stdin = bytes.NewReader(userYAML)
		cmd.Stdout = prefix("key-bootstrap:\t", os.Stdout)
		cmd.Stderr = prefix("key-bootstrap:\t", os.Stderr)
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	// Start other servers.
	for i := range sc.Servers {
		s := sc.Servers[i]
		if s.Name == "keyserver" {
			continue
		}

		cmd, err := sc.startServer(tmpDir, logLevel, s)
		if err != nil {
			return err
		}
		defer kill(cmd)
	}
	// Wait for the other servers to start.
	for _, s := range sc.Servers {
		if s.Name == "keyserver" {
			continue
		}
		if err := waitReady(s.addr); err != nil {
			return err
		}
	}

	// Start a shell as the first user.
	configFile, err = sc.writeConfig(tmpDir, "shell", sc.Users[0].Name)
	if err != nil {
		return err
	}
	args = []string{
		"-config=" + configFile,
		"-log=" + logLevel,
		"shell",
	}
	fmt.Fprintf(os.Stderr, "upbox: upspin %s\n", strings.Join(args, " "))
	shell := exec.Command("upspin", args...)
	shell.Stdin = os.Stdin
	shell.Stdout = os.Stdout
	shell.Stderr = os.Stderr
	return shell.Run()
}

// writeConfig writes a config file for user named "config.name" inside dir.
func (sc *Schema) writeConfig(dir, name, user string) (string, error) {
	u, ok := sc.user[user]
	if !ok {
		return "", fmt.Errorf("unknown user %q", user)
	}

	configContent := []string{
		"username: " + u.Name,
		"secrets: " + filepath.Join(dir, user),
		"tlscerts: " + dir,
		"packing: " + u.Packing,
		"storeserver: " + u.StoreServer,
		"dirserver: " + u.DirServer,
	}
	switch name {
	case "keyserver":
		configContent = append(configContent,
			"keyserver: inprocess,",
		)
	default:
		configContent = append(configContent,
			"keyserver: remote,"+sc.KeyServer,
		)
	}
	configFile := filepath.Join(dir, "config."+name)
	if err := ioutil.WriteFile(configFile, []byte(strings.Join(configContent, "\n")), 0644); err != nil {
		return "", err
	}
	return configFile, nil
}

// startServer writes a config file for the given server's user
// and starts the server, returning the running exec.Cmd.
func (sc *Schema) startServer(dir, logLevel string, s *Server) (*exec.Cmd, error) {
	configFile, err := sc.writeConfig(dir, s.Name, s.User)
	if err != nil {
		return nil, fmt.Errorf("writing config for %v: %v", s.Name, err)
	}

	args := []string{
		"-config=" + configFile,
		"-log=" + logLevel,
		"-tls_cert=" + filepath.Join(dir, "cert.pem"),
		"-tls_key=" + filepath.Join(dir, "key.pem"),
		"-letscache=", // disable
		"-https=" + s.addr,
		"-addr=" + s.addr,
	}
	if s.Name == "keyserver" {
		args = append(args,
			"-test_user="+s.User,
			"-test_secrets="+filepath.Join(dir, s.User),
		)
	}
	for k, v := range s.Flags {
		args = append(args, fmt.Sprintf("-%s=%v", k, v))
	}
	cmd := exec.Command(s.Name, args...)
	cmd.Stdout = prefix(s.Name+":\t", os.Stdout)
	cmd.Stderr = prefix(s.Name+":\t", os.Stderr)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %v: %v", s.Name, err)
	}
	return cmd, nil
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
