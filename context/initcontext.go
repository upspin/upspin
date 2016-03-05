//Package context creates a client context from various sources.
package context

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/user"
	"path"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/key/keyloader"
	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

// InitContext returns a context generated from configuration files and environment variables.
// If passed a non-nil io.Reader, that is used instead of the default files.
func InitContext(r io.Reader) (*upspin.Context, error) {
	vals := map[string]string{"name": "noone@nowhere.org",
		"user":      "inprocess",
		"directory": "inprocess",
		"store":     "inprocess",
		"packing":   "plain"}

	if r == nil {
		dir := "/etc/upspin"
		if u, err := user.Current(); err == nil {
			if len(u.HomeDir) != 0 {
				dir = path.Join(u.HomeDir, ".upspin")
			}
		}
		if f, err := os.Open(path.Join(dir, "config")); err == nil {
			r = f
			defer f.Close()
		}
	}

	// First source of truth is the RC file.
	if r != nil {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			tokens := strings.SplitN(line, "=", 2)
			if len(tokens) != 2 {
				continue
			}
			val := strings.Trim(tokens[1], " \t")
			attr := strings.Trim(tokens[0], " \t")
			if _, ok := vals[attr]; ok {
				vals[attr] = val
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	// Environment variables trump the RC file.
	for k := range vals {
		if v := os.Getenv("upspin" + k); len(v) != 0 {
			vals[k] = v
		}
	}

	context := new(upspin.Context)
	context.UserName = upspin.UserName(vals["name"])
	packer := pack.LookupByName(vals["packing"])
	if packer == nil {
		return nil, fmt.Errorf("unknown packing %s", vals["packing"])
	}
	context.Packing = packer.Packing()
	var err error
	var ep *upspin.Endpoint
	if ep, err = endpoint.Parse(vals["user"]); err != nil {
		return nil, err
	}
	if context.User, err = access.BindUser(context, *ep); err != nil {
		return nil, err
	}
	if ep, err = endpoint.Parse(vals["store"]); err != nil {
		return nil, err
	}
	if context.Store, err = access.BindStore(context, *ep); err != nil {
		return nil, err
	}
	if ep, err = endpoint.Parse(vals["directory"]); err != nil {
		return nil, err
	}
	if context.Directory, err = access.BindDirectory(context, *ep); err != nil {
		return nil, err
	}
	// Implicitly load the user's keys from $HOME/.ssh.
	// TODO: add a section in vals containing overrides for "publickey" and "privatekey" files.
	keyloader.Load(context)
	return context, nil
}
