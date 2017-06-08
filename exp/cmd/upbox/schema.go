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
func SchemaFromFile(name string) (*Schema, error) {
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

	port := *basePort
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
