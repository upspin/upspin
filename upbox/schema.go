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
	  cache: true
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
	dir: /path/to/upbox/state


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

Cache is a boolean that specifies whether to start a cacheserver for this user.

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

Generated data

A running Schema needs to store data on disk: config files, TLS certificates,
and dirserver log data (if a dirserver of kind 'server' is selected).
By default, these files are kept in a temporary directory that is created by
the Start method and removed by the Stop method.

If the "dir" property is set, upbox will use that path to store its data and
will not clean up inside Stop. Upon a restart, upbox will use whatever it finds,
filling in any missing gaps in regards to the schema. If persistent storage
(eg. 'disk') is used, one can resume a previously started session.
An example schema for a resumable session:

	dir: /tmp/upbox
	users:
	- name: john
	servers:
	- name: storeserver
	  flags:
	    kind: server
	- name: dirserver
	  flags:
	    kind: server
	- name: keyserver
	domain: local.host

*/
package upbox // import "upspin.io/upbox"

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"upspin.io/config"
	"upspin.io/log"
	"upspin.io/rpc/local"
	"upspin.io/test/testutil"
	"upspin.io/upspin"

	yaml "gopkg.in/yaml.v2"
)

// Schema defines a set of Upspin Users and Servers.
type Schema struct {
	Users   []*User
	Servers []*Server

	// Dir specifies the directory in which to store the config files and keys.
	// Any data that may already be present there will be reused. This allows
	// restoring previous sessions. If unspecified, a temporary directory
	// will be used.
	Dir string

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

	// cleanup indicates whether the config files and keys generated by
	// upbox should be deleted when upbox stops.
	cleanup bool

	// started indicates whether upbox has started.
	started bool

	// session specifies information about a previously started upbox session.
	// It is read from and written to session.json in Dir.
	session session
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

	// Cache specifies whether to run a cacheserver for this user.
	Cache bool

	secrets string // path to user's public and private keys; set by Run

	cacheserver *exec.Cmd
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
func SchemaFromFile(name string) (*Schema, error) {
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
	return SchemaFromYAML(doc)
}

// SchemaFromYAML parses a Schema from the given YAML document.
func SchemaFromYAML(doc string) (*Schema, error) {
	var sc Schema
	if err := yaml.Unmarshal([]byte(doc), &sc); err != nil {
		return nil, err
	}

	sc.user = map[string]*User{}
	sc.server = map[string]*Server{}

	if len(sc.Users) == 0 {
		return nil, errors.New("at least one user must be specified")
	}

	if sc.Dir != "" {
		if err := sc.session.fromDir(sc.Dir); err != nil {
			return nil, fmt.Errorf("error reading session file: %v", err)
		}
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

		// Pick or restore address for this service.
		var err error
		if s.addr, err = sc.session.serverAddress(s.Name); err != nil {
			return nil, fmt.Errorf("error getting server address: %v", err)
		}

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

// sessionFile specifies the name of the file holding session information.
const sessionFile = "session"

// session holds information on a started upbox session.
type session struct {
	// ServerAddr maps server names to the addresses that they should
	// be listening on.
	ServerAddr map[string]string
}

// fromDir attempts to read session information from the given directory.
func (s *session) fromDir(dir string) error {
	b, err := ioutil.ReadFile(filepath.Join(dir, sessionFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(b, s)
}

// toDir attempts to save the current session data to the given directory.
func (s *session) toDir(dir string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(dir, sessionFile), b, 0644)
}

// setServerAddress sets the address that the given server is listening
// on in this session.
func (s *session) setServerAddress(server, addr string) {
	if s.ServerAddr == nil {
		s.ServerAddr = make(map[string]string)
	}
	s.ServerAddr[server] = addr
}

// serverAddress returns the address that the given server
// should be listening on during this session.
func (s *session) serverAddress(server string) (string, error) {
	if addr, ok := s.ServerAddr[server]; ok {
		return addr, nil
	}
	port, err := testutil.PickPort()
	if err != nil {
		return "", err
	}
	s.setServerAddress(server, "localhost:"+port)
	return s.serverAddress(server)
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

// pathExists returns true if the path p exists.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}

// Command returns the path for the given command executable.
func (sc *Schema) Command(name string) string {
	return filepath.Join(sc.Dir, name)
}

// Start sets up the Users and Servers specified by the Schema.
func (sc *Schema) Start() error {
	if sc.Dir == "" {
		// No directory set, so we use a temporary one.
		tmpDir, err := ioutil.TempDir("", "upbox")
		if err != nil {
			return err
		}
		sc.Dir = tmpDir
		sc.cleanup = true
	} else if !pathExists(sc.Dir) {
		// A directory is set, but it doesn't exist. Create it.
		if err := os.MkdirAll(sc.Dir, 0700); err != nil {
			return err
		}
	}

	// Build servers and commands.
	cmds := []string{
		"upspin.io/cmd/upspin",
		"upspin.io/cmd/cacheserver",
	}
	for _, s := range sc.Servers {
		cmds = append(cmds, s.ImportPath)
	}
	for _, p := range cmds {
		cmd := exec.Command("go", "build", "-o", sc.Command(path.Base(p)), p)
		cmd.Stdout = prefix("build: ", os.Stdout)
		cmd.Stderr = prefix("build: ", os.Stderr)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%q build error: %v", p, err)
		}
	}

	sc.started = true

	// If anything below fails, we should shut down.
	shutdown := true
	defer func() {
		if shutdown {
			sc.Stop()
		}
	}()

	// Generate TLS certificates.
	if err := generateCert(sc.Dir); err != nil {
		return err
	}

	// Generate keys.
	for _, u := range sc.Users {
		dir := filepath.Join(sc.Dir, u.Name)
		u.secrets = dir
		if pathExists(dir) {
			// The keys are already there from a pervious install.
			log.Debug.Printf("found keys: %s", dir)
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		var buf bytes.Buffer
		keygen := exec.Command(sc.Command("upspin"), "keygen", dir)
		keygen.Stdout = prefix("keygen: ", &buf)
		keygen.Stderr = prefix("keygen: ", &buf)
		if err := keygen.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "%s", buf.Bytes())
			return err
		}
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
		pk, err := ioutil.ReadFile(filepath.Join(sc.Dir, u.Name, "public.upspinkey"))
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
		cmd := exec.Command(sc.Command("upspin"),
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
	// Start cacheservers.
	for _, u := range sc.Users {
		if !u.Cache {
			continue
		}
		cmd := exec.Command(sc.Command("cacheserver"),
			"-config="+sc.Config(u.Name),
			"-log="+sc.logLevel(),
			"-cachedir="+sc.Dir,
		)
		p := "cacheserver." + u.Name + ":\t"
		cmd.Stdout = prefix(p, os.Stdout)
		cmd.Stderr = prefix(p, os.Stderr)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting cacheserver for %v: %v", u.Name, err)
		}
		u.cacheserver = cmd

		if err := waitReady(u.cacheAddr()); err != nil {
			return err
		}
	}

	if err := sc.session.toDir(sc.Dir); err != nil {
		return err
	}

	shutdown = false // Exiting successfully, so don't clean up.
	return nil
}

// Stop terminates any running server processes and deletes all
// temporary files.
func (sc *Schema) Stop() error {
	if !sc.started {
		return errors.New("cannot stop; not started")
	}
	// Kill running servers and Wait for them to stop.
	var cmds []*exec.Cmd
	for _, s := range sc.Servers {
		cmds = append(cmds, s.cmd)
	}
	for _, u := range sc.Users {
		cmds = append(cmds, u.cacheserver)
	}
	var wg sync.WaitGroup
	for _, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			wg.Add(1)
			go func(cmd *exec.Cmd) {
				defer wg.Done()
				cmd.Wait()
			}(cmd)
			cmd.Process.Kill()
		}
	}
	wg.Wait()
	sc.started = false
	if sc.cleanup {
		return os.RemoveAll(sc.Dir)
	}
	return nil
}

// Config returns the path to the config for the given user.
func (sc *Schema) Config(user string) string {
	return filepath.Join(sc.Dir, "config."+user)
}

// writeConfig writes a config file for user named "config.name" inside dir.
func (sc *Schema) writeConfig(user string) error {
	u, ok := sc.user[user]
	if !ok {
		return fmt.Errorf("unknown user %q", user)
	}
	filename := sc.Config(u.Name)
	if pathExists(filename) {
		log.Debug.Printf("found config: %s", filename)
		return nil
	}

	cfg := []string{
		"username: " + u.Name,
		"secrets: " + filepath.Join(sc.Dir, u.Name),
		"tlscerts: " + sc.Dir,
		"packing: " + u.Packing,
		"storeserver: " + u.StoreServer,
		"dirserver: " + u.DirServer,
	}
	switch user {
	case "keyserver":
		cfg = append(cfg,
			"keyserver: inprocess,",
		)
	default:
		cfg = append(cfg,
			"keyserver: remote,"+sc.KeyServer,
		)
	}
	if u.Cache {
		cfg = append(cfg, "cache: "+u.cacheAddr())
	}
	cfg = append(cfg, "") // trailing \n
	return ioutil.WriteFile(filename, []byte(strings.Join(cfg, "\n")), 0644)
}

func (u *User) cacheAddr() string {
	return config.LocalName(
		config.SetUserName(config.New(), upspin.UserName(u.Name)),
		"upbox.cacheserver",
	) + ":80"
}

// startServer starts the given server, returning the running exec.Cmd.
func (sc *Schema) startServer(s *Server) (*exec.Cmd, error) {
	args := []string{
		"-config=" + sc.Config(s.User),
		"-log=" + sc.logLevel(),
		"-tls_cert=" + filepath.Join(sc.Dir, "cert.pem"),
		"-tls_key=" + filepath.Join(sc.Dir, "key.pem"),
		"-letscache=", // disable
		"-https=" + s.addr,
		"-addr=" + s.addr,
	}
	if s.Name == "keyserver" {
		args = append(args,
			"-test_user="+s.User,
			"-test_secrets="+filepath.Join(sc.Dir, s.User),
		)
	}
	_, hasServerConfigFlag := s.Flags["serverconfig"]
	for k, v := range s.Flags {
		args = append(args, fmt.Sprintf("-%s=%v", k, v))
		if !hasServerConfigFlag && k == "kind" && v == "server" {
			switch s.Name {
			case "dirserver":
				args = append(args, "-serverconfig", "logDir="+filepath.Join(sc.Dir, "logdir"))
			case "storeserver":
				args = append(args, "-serverconfig", "backend=Disk,basePath="+filepath.Join(sc.Dir, "storage"))
			}
		}
	}
	cmd := exec.Command(sc.Command(path.Base(s.ImportPath)), args...)
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
	url := "https://" + addr
	if config.IsLocal(addr) {
		url = "http://" + addr
	}
	rt := &http.Transport{
		DialContext: (&local.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 5 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	req, _ := http.NewRequest("GET", url, nil)
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
