// +build ign

package main

import (
	"fmt"
	"log"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/client/testclient"
	"upspin.googlesource.com/upspin.git/directory/testdir"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/testuser"
)

// TODO: Copied from testdirectory/all_test.go. Make this publicly available.

// Avoid networking for now.
const testAddr = "test:0.0.0.0"

type Context string

func (c Context) Name() string {
	return string(c)
}

var _ upspin.ClientContext = (*Context)(nil)

type Setup struct {
	upspin.User
	upspin.Store
	upspin.Directory
}

func setup() (*Setup, error) {
	ctxt := Context("testcontext")
	loc := upspin.Location{
		Transport: "in-process",
		NetAddr:   testAddr,
		Reference: upspin.Reference{
			Key:     "unused",
			Packing: upspin.Debug,
		},
	}
	us, err := access.Switch.BindUser(ctxt, loc)
	if err != nil {
		return nil, err
	}
	ds, err := access.Switch.BindDirectory(ctxt, loc)
	if err != nil {
		return nil, err
	}
	// HACK: We set the store for the blobs to be the same as for the directory.
	return &Setup{
		User:      us,
		Store:     ds.(*testdir.Service).Store,
		Directory: ds,
	}, nil
}

// TODO: End of copied code.

const (
	user = "user@google.com"
	root = user + "/"
	file = root + "file"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("ehg: ")
	s, err := setup()
	if err != nil {
		log.Fatal(err)
	}
	client := testclient.New(s.Directory, s.Store)
	_, err = client.MakeDirectory(root)
	if err != nil {
		log.Fatal("make directory:", err)
	}
	_, err = client.Put(file, []byte("A massage from the Swedish Prime Minister"))
	if err != nil {
		log.Fatal("put file:", err)
	}
	data, err := client.Get(file) // TODO: Metadata?
	if err != nil {
		log.Fatal("get file:", err)
	}
	fmt.Printf("%s\n", data)
}
