// Package setup provides a declarative framework for creating a complete Upspin test environment.
// See setup_test.go for an example on how to use it.
package setup

import (
	"errors"
	"log"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/client"
	"upspin.googlesource.com/upspin.git/pack/ee"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/user/testuser"
)

// E is an entry in the Upspin namespace. Its name and fields are intentionally short for ease in testing.
type E struct {
	// P is a path name in string format. Directories must end in "/".
	P string

	// C is the contents of P. C must be empty for directories.
	C string
}

// Setup is a full directory tree with path names and their contents.
type Setup []E

// Create creates a new directory tree as per setup using the given client. If verbose is true, debug messages are logged.
func Create(client upspin.Client, verbose bool, setup Setup) error {
	if client == nil {
		return errors.New("client must not be nil")
	}
	for _, e := range setup {
		if strings.HasSuffix(e.P, "/") {
			if len(e.C) > 0 {
				return errors.New("directory entry must not have contents")
			}
			dir := upspin.PathName(e.P)
			loc, err := client.MakeDirectory(dir)
			if err != nil {
				if verbose {
					log.Printf("Setup: Error creating directory %s: %s", dir, err)
				}
				return err
			}
			if verbose {
				log.Printf("Setup: Created dir %s at %v", dir, loc)
			}
		} else {
			name := upspin.PathName(e.P)
			loc, err := client.Put(name, []byte(e.C))
			if err != nil {
				if verbose {
					log.Printf("Error creating file %s: %s", name, err)
				}
				return err
			}
			if verbose {
				log.Printf("Setup: Created file %s at %v", name, loc)
			}
		}
	}
	if verbose {
		log.Printf("Setup: All entries created.")
	}
	return nil
}

// N is a helper function (short for New) that creates an entry E without resorting to using composite literal with
// unkeyed fields, which is a lint stylistic error.
func N(pathName string, contents string) E {
	return E{
		P: pathName,
		C: contents,
	}
}

// NewTestGCP returns a Client and a Context pointing to the GCP test instances on upspin.io.
// The Client and Context are not bound to any user yet. Use AddUser for that.
func NewTestGCP() (upspin.Client, *upspin.Context, error) {
	// Use a test GCP Store...
	endpointStore := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "https://upspin.io:9980", // Test store server.
	}
	// ... and a test GCP directory ...
	endpointDir := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   "https://upspin.io:9981", // Test dir server.
	}
	// and an in-process test user.
	endpointUser := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "", // ignored.
	}
	context, err := newContext(endpointStore, endpointDir, endpointUser)
	client := client.New(context)
	return client, context, err
}

// NewInprocess returns a Client and a Context pointing to in-process instances.
// The Client and Context are not bound to any user yet. Use AddUser for that.
func NewInprocess() (upspin.Client, *upspin.Context, error) {
	// Use an in-process Store...
	endpointStore := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	// ... and an in-process directory ...
	endpointDir := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	// and an in-process test user.
	endpointUser := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	context, err := newContext(endpointStore, endpointDir, endpointUser)
	client := client.New(context)
	return client, context, err
}

// AddUser adds a new user to the user service, with a newly-created key depending on the chosen packing type.
// It returns the new keys and updates the context with packing, keys and the new user as the current user.
func AddUser(userName upspin.UserName, context *upspin.Context, packing upspin.Packing) (*upspin.KeyPair, error) {
	context.Packing = packing
	context.UserName = userName
	entropy := make([]byte, 32) // Enough for p521
	ee.GenEntropy(entropy)
	// Do a switch packing here when we have other key types that are not ee. Debug and Plain don't care about keys.
	keyPair, err := ee.CreateKeys(packing, entropy)
	if err != nil {
		return nil, err
	}
	context.KeyPair = *keyPair

	testUser, ok := context.User.(*testuser.Service)
	if !ok {
		return nil, errors.New("not a test user in context")
	}
	// Set the public key for the current user.
	testUser.SetPublicKeys(userName, []upspin.PublicKey{keyPair.Public})
	err = testUser.Install(userName, context.Directory)
	if err != nil && !strings.Contains(err.Error(), "already ") {
		return nil, err
	}
	return keyPair, nil
}

func newContext(store, dir, user upspin.Endpoint) (*upspin.Context, error) {
	context := &upspin.Context{}
	var err error
	context.Store, err = bind.Store(context, store)
	if err != nil {
		return nil, err
	}
	context.Directory, err = bind.Directory(context, dir)
	if err != nil {
		return nil, err
	}
	context.User, err = bind.User(context, user)
	if err != nil {
		return nil, err
	}
	return context, nil
}
