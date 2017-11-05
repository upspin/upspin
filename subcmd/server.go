// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Server helpers.

package subcmd

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"upspin.io/upspin"
)

// ServerConfig describes the configuration of an upspinserver.
type ServerConfig struct {
	// Addr specifies the public host and port of the upspinserver.
	Addr upspin.NetAddr

	// User specifies the user name that the upspinserver will run as.
	User upspin.UserName

	// StoreConfig specifies the configuration options for the StoreServer.
	StoreConfig []string
}

// ServerConfigFile specifies the file name of the JSON-encoded ServerConfig.
const ServerConfigFile = "serverconfig.json"

// SetupServerFiles specifies the configuration files that 'upspin setupserver'
// should send to the upspinserver.
var SetupServerFiles = []string{
	"Writers",
	"public.upspinkey",
	"secret.upspinkey",
	ServerConfigFile,
}

// ReadServerConfig reads and JSON-decodes the ServerConfigFile under cfgPath.
func (s *State) ReadServerConfig(cfgPath string) *ServerConfig {
	cfgFile := filepath.Join(cfgPath, ServerConfigFile)
	b, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		if os.IsNotExist(err) {
			s.Exitf("No server config file found at %q.\nRun 'upspin setupdomain' first.", cfgFile)
		}
		s.Exit(err)
	}
	cfg := &ServerConfig{}
	if err := json.Unmarshal(b, cfg); err != nil {
		s.Exit(err)
	}
	return cfg
}

// WriteServerConfig JSON-encodes and writes the ServerConfigFile under cfgPath.
func (s *State) WriteServerConfig(cfgPath string, cfg *ServerConfig) {
	cfgFile := filepath.Join(cfgPath, ServerConfigFile)
	b, err := json.Marshal(cfg)
	if err != nil {
		s.Exit(err)
	}
	err = ioutil.WriteFile(cfgFile, b, 0644)
	if err != nil {
		s.Exit(err)
	}
}
