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
package upbox // import "upspin.io/upbox"

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
	"sync"
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

	// LogLevel specifies the logging level that each server should use.
	LogLevel string

	// user and server are mappings of user names into the Users and
	// Servers slices. They are set by SchemaFromYAML.
	user   map[string]*User
	server map[string]*Server

	// dir is the temporary directory in which the
	// user config files and keys were written by Start.
	dir string
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

	addr string // the host:port of this server; set by SchemaFromYAML

	cmd *exec.Cmd // the running process; set by Start
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
	var doc string
	if name == "" {
		doc = DefaultSchema
	} else {
		data, err := ioutil.ReadFile(name)
		if err != nil {
			return nil, err
		}
		doc = string(data)
	}
	return SchemaFromYAML(doc, basePort)
}

// SchemaFromYAML parses a Schema from the given YAML document.
func SchemaFromYAML(doc string, basePort int) (*Schema, error) {
	var sc Schema
	if err := yaml.Unmarshal([]byte(doc), &sc); err != nil {
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

// Start sets up the Users and Servers specified by the Schema.
func (sc *Schema) Start() error {
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
	sc.dir = tmpDir

	// If anything below fails, we should clean up.
	cleanup := true
	defer func() {
		if cleanup {
			sc.Stop()
		}
	}()

	// Generate TLS certificates.
	if err := generateCert(sc.dir); err != nil {
		return err
	}

	// Generate keys.
	for _, u := range sc.Users {
		dir := filepath.Join(sc.dir, u.Name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		var buf bytes.Buffer
		keygen := exec.Command("upspin", "keygen", dir)
		keygen.Stdout = prefix("keygen: ", &buf)
		keygen.Stderr = prefix("keygen: ", &buf)
		if err := keygen.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "%s", buf.Bytes())
			return err
		}
		u.secrets = dir
	}

	keyUser := ""
	if s, ok := sc.server["keyserver"]; ok {
		keyUser = s.User
		// Start keyserver.
		if err := sc.writeConfig(keyUser); err != nil {
			return err
		}
		cmd, err := sc.startServer(s)
		if err != nil {
			return err
		}
		s.cmd = cmd
	}
	// Wait for the keyserver to start and add the users to it.
	if err := waitReady(sc.KeyServer); err != nil {
		return err
	}
	for _, u := range sc.Users {
		if u.Name == keyUser {
			continue
		}

		if err := sc.writeConfig(u.Name); err != nil {
			return fmt.Errorf("writing config for %v: %v", u.Name, err)
		}

		if keyUser == "" {
			continue
		}
		pk, err := ioutil.ReadFile(filepath.Join(sc.dir, u.Name, "public.upspinkey"))
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
			"-config="+sc.Config(keyUser),
			"-log="+sc.logLevel(),
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

		cmd, err := sc.startServer(s)
		if err != nil {
			return err
		}
		s.cmd = cmd
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

	cleanup = false // Exiting successfully, so don't clean up.
	return nil
}

// Stop terminates any running server processes and
// deletes all temporary config files and keys.
func (sc *Schema) Stop() error {
	if sc.dir == "" {
		return errors.New("cannot stop; not started")
	}
	// Kill running servers and Wait for them to stop.
	var wg sync.WaitGroup
	for _, s := range sc.Servers {
		if s.cmd != nil && s.cmd.Process != nil {
			wg.Add(1)
			go func(cmd *exec.Cmd) {
				defer wg.Done()
				cmd.Wait()
			}(s.cmd)
			s.cmd.Process.Kill()
		}
	}
	wg.Wait()
	return os.RemoveAll(sc.dir)
}

// Config returns the path to the config for the given user.
func (sc *Schema) Config(user string) string {
	return filepath.Join(sc.dir, "config."+user)
}

// writeConfig writes a config file for user named "config.name" inside dir.
func (sc *Schema) writeConfig(user string) error {
	u, ok := sc.user[user]
	if !ok {
		return fmt.Errorf("unknown user %q", user)
	}

	configContent := []string{
		"username: " + u.Name,
		"secrets: " + filepath.Join(sc.dir, u.Name),
		"tlscerts: " + sc.dir,
		"packing: " + u.Packing,
		"storeserver: " + u.StoreServer,
		"dirserver: " + u.DirServer,
	}
	switch user {
	case "keyserver":
		configContent = append(configContent,
			"keyserver: inprocess,",
		)
	default:
		configContent = append(configContent,
			"keyserver: remote,"+sc.KeyServer,
		)
	}
	return ioutil.WriteFile(sc.Config(u.Name), []byte(strings.Join(configContent, "\n")), 0644)
}

// startServer starts the given server, returning the running exec.Cmd.
func (sc *Schema) startServer(s *Server) (*exec.Cmd, error) {
	args := []string{
		"-config=" + sc.Config(s.User),
		"-log=" + sc.logLevel(),
		"-tls_cert=" + filepath.Join(sc.dir, "cert.pem"),
		"-tls_key=" + filepath.Join(sc.dir, "key.pem"),
		"-letscache=", // disable
		"-https=" + s.addr,
		"-addr=" + s.addr,
	}
	if s.Name == "keyserver" {
		args = append(args,
			"-test_user="+s.User,
			"-test_secrets="+filepath.Join(sc.dir, s.User),
		)
	}
	_, hasServerConfigFlag := s.Flags["serverconfig"]
	for k, v := range s.Flags {
		args = append(args, fmt.Sprintf("-%s=%v", k, v))
		if s.Name == "dirserver" && k == "kind" && v == "server" && !hasServerConfigFlag {
			args = append(args, "-serverconfig", "logDir="+sc.dir)
		}
	}
	cmd := exec.Command(s.Name, args...)
	cmd.Stdout = prefix(s.Name+":\t", os.Stdout)
	cmd.Stderr = prefix(s.Name+":\t", os.Stderr)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %v: %v", s.Name, err)
	}
	return cmd, nil
}

func (sc *Schema) logLevel() string {
	if l := sc.LogLevel; l != "" {
		return l
	}
	return "error"
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
