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

const serverConfigFile = "serverconfig.json"

var ConfigureServerFiles = []string{
	"Writers",
	"public.upspinkey",
	"secret.upspinkey",
	"serviceaccount.json",
	serverConfigFile,
}

var OptionalConfigureServerFiles = map[string]bool{
	"serviceaccount.json": true,
}

func (s *State) ReadServerConfig(cfgPath string) *ServerConfig {
	cfgFile := filepath.Join(cfgPath, serverConfigFile)
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

func (s *State) WriteServerConfig(cfgPath string, cfg *ServerConfig) {
	cfgFile := filepath.Join(cfgPath, serverConfigFile)
	b, err := json.Marshal(cfg)
	if err != nil {
		s.Exit(err)
	}
	err = ioutil.WriteFile(cfgFile, b, 0644)
	if err != nil {
		s.Exit(err)
	}
}

type ServerConfig struct {
	Addr   upspin.NetAddr
	User   upspin.UserName
	Bucket string
}
