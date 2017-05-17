// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main // import "upspin.io/exp/cmd/upbox"

import (
	"errors"
	"fmt"
	"io/ioutil"
	"strings"

	yaml "gopkg.in/yaml.v2"
)

// Config defines an Upspin configuration of Users and Servers.
type Config struct {
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

// User defines an Upspin user to be created and used within a configuration.
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

// Server defines an Upspin server to be created and used within a configuration.
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

// DefaultConfig is the configuration that is used if no configuration is provided.
const DefaultConfig = `
users:
  - name: user
servers:
  - name: keyserver
  - name: storeserver
  - name: dirserver
domain: example.com
`

// ConfigFromFile parses a Config from the named file.
// If no name is provided the DefaultConfig is used.
func ConfigFromFile(name string) (*Config, error) {
	var data []byte
	if name == "" {
		data = []byte(DefaultConfig)
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

	cfg.user = map[string]*User{}
	cfg.server = map[string]*Server{}

	if len(cfg.Users) == 0 {
		return nil, errors.New("at least one user must be specified")
	}

	// Add domain to usernames without domains,
	// and default user names for servers.
	for i, u := range cfg.Users {
		if u.Name == "" {
			return nil, fmt.Errorf("user[%d] must specify a name", i)
		}

		// Add domain to bare user name.
		if !strings.Contains(u.Name, "@") {
			if cfg.Domain == "" {
				return nil, fmt.Errorf("user %q implies domain suffix, but domain not set", u.Name)
			}
			u.Name += "@" + cfg.Domain
		}
		if u.Packing == "" {
			u.Packing = "ee"
		}

		// Add to map only after name has been normalized.
		cfg.user[u.Name] = u
	}

	port := *basePort
	for i, s := range cfg.Servers {
		if s.Name == "" {
			return nil, fmt.Errorf("server[%d] must specify a name", i)
		}
		cfg.server[s.Name] = s

		if s.User == "" {
			// If no user specified, default to server@domain.
			if cfg.Domain == "" {
				return nil, fmt.Errorf("server %q specifies no user, but domain must be specified to create default user", s.Name)
			}
			s.User = s.Name + "@" + cfg.Domain
			// If the user isn't otherwise provided, create it.
			if _, ok := cfg.user[s.User]; !ok {
				u := newUserFor(s)
				cfg.Users = append(cfg.Users, u)
				cfg.user[u.Name] = u
			}
		} else if !strings.Contains(s.User, "@") {
			// Add the domain name if user is specified.
			if cfg.Domain == "" {
				return nil, fmt.Errorf("server %q specifies user %q without domain suffix, but domain not set", s.Name, s.User)
			}
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

	// Check for KeyServer only after we may have set it as "keyserver" above.
	if cfg.KeyServer == "" {
		return nil, errors.New("no keyserver in configuration")
	}

	// Set or evaluate DirServer and StoreServer fields.
	for _, u := range cfg.Users {
		if err := setServer(&cfg, &u.DirServer, "dirserver"); err != nil {
			return nil, fmt.Errorf("user %q: %v", u.Name, err)
		}
		if err := setServer(&cfg, &u.StoreServer, "storeserver"); err != nil {
			return nil, fmt.Errorf("user %q: %v", u.Name, err)
		}
	}

	return &cfg, nil
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

func setServer(cfg *Config, field *string, kind string) error {
	if *field == "" {
		s, ok := cfg.server[kind]
		if !ok {
			return fmt.Errorf("needs default %s, but none found", kind)
		}
		*field = "remote," + s.addr
	} else if (*field)[0] == '$' {
		name := (*field)[1:]
		s, ok := cfg.server[name]
		if !ok {
			return fmt.Errorf("specifies %v %q, but none found", kind, name)
		}
		*field = "remote," + s.addr
	}
	return nil
}
