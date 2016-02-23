//Package context creates a client context from various sources.
package context

import (
	"bufio"
	"io"
	"os"
	"os/user"
	"path"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/upspin"
)

// InitContext returns a context generated from configuration files and environment variables.
// If passed an io reader, that is used instead of the default files.
func InitContext(r io.Reader) *upspin.Context {
	vals := map[string]string{"name": "noone@nowhere.org",
		"user":      "InProcess,",
		"directory": "InProcess,",
		"store":     "",
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
	}

	// Environment variables trump the RC file.
	for k := range vals {
		if v := os.Getenv("upspin" + k); len(v) != 0 {
			vals[k] = v
		}
	}

	context := new(upspin.Context)
	context.UserName = upspin.UserName(vals["name"])
	context.Packing = parsePacking(vals["packing"])
	var err error
	context.User, err = access.BindUser(context, parseEndpoint(vals["user"]))
	if err != nil {
		panic(err)
	}
	context.Store, err = access.BindStore(context, parseEndpoint(vals["store"]))
	if err != nil {
		panic(err)
	}
	context.Directory, err = access.BindDirectory(context, parseEndpoint(vals["directory"]))
	if err != nil {
		panic(err)
	}
	return context
}

func parseEndpoint(v string) upspin.Endpoint {
	elems := strings.SplitN(v, ",", 2)
	if len(elems) < 2 {
		return upspin.Endpoint{Transport: upspin.InProcess, NetAddr: upspin.NetAddr("")}
	}
	switch elems[0] {
	case "http":
		return upspin.Endpoint{Transport: upspin.HTTP, NetAddr: upspin.NetAddr(elems[1])}
	case "gcp":
		return upspin.Endpoint{Transport: upspin.GCP, NetAddr: upspin.NetAddr(elems[1])}
	}
	return upspin.Endpoint{Transport: upspin.InProcess, NetAddr: upspin.NetAddr(elems[1])}
}

func parsePacking(v string) upspin.Packing {
	switch v {
	case "debug":
		return upspin.DebugPack
	case "plain":
		return upspin.PlainPack
	case "eep256":
		return upspin.EEp256Pack
	}
	return upspin.PlainPack
}
