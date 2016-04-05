// Package testsetup provides a declarative framework for creating a complete Upspin test environment.
// See testsetup_test.go for an example on how to use it.
package testsetup

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

// Entry is an entry in the Upspin namespace.
type Entry struct {
	// P is a path name in string format. Directories must end in "/".
	P string

	// C is the contents of P. C must be empty for directories.
	C string
}

// Setup is a full directory tree with path names and their contents.
type Setup []Entry

// Tree creates a new directory tree as per setup using the given client. If verbose is true, debug messages are logged.
func Tree(client upspin.Client, verbose bool, setup Setup) error {
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
					log.Printf("Tree: Error creating directory %s: %s", dir, err)
				}
				return err
			}
			if verbose {
				log.Printf("Tree: Created dir %s at %v", dir, loc)
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
				log.Printf("Tree: Created file %s at %v", name, loc)
			}
		}
	}
	if verbose {
		log.Printf("Tree: All entries created.")
	}
	return nil
}

// N (short for New) is a helper function to return a new Entry.
func N(pathName string, contents string) Entry {
	return Entry{
		P: pathName,
		C: contents,
	}
}

// GCP returns a Client and a Context pointing to the GCP test instances on upspin.io.
// The Client and Context are not bound to any user yet. Use AddUser for that.
func GCP() (upspin.Client, *upspin.Context, error) {
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

// InProcess returns a Client and a Context pointing to in-process instances.
// The Client and Context are not bound to any user yet. Use AddUser for that.
func InProcess() (upspin.Client, *upspin.Context, error) {
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

// AddUser adds a new user to the user service bound in the context and creates a new key for the user based on
// the chosen packing type. The context is updated with the user, keys and packing.
func AddUser(context *upspin.Context, userName upspin.UserName, packing upspin.Packing) error {
	context.UserName = userName
	context.Packing = packing

	entropy := make([]byte, 32) // Enough for p521
	ee.GenEntropy(entropy)
	var keyPair *upspin.KeyPair
	var err error
	switch packing {
	case upspin.EEp256Pack, upspin.EEp384Pack, upspin.EEp521Pack:
		keyPair, err = ee.CreateKeys(packing, entropy)
	default:
		// For non-EE packing, a p256 key will do.
		keyPair, err = ee.CreateKeys(upspin.EEp256Pack, entropy)
	}
	if err != nil {
		return err
	}
	context.KeyPair = *keyPair

	testUser, ok := context.User.(*testuser.Service)
	if !ok {
		return errors.New("user service must be the in-process instance")
	}
	// Set the public key for the current user.
	testUser.SetPublicKeys(userName, []upspin.PublicKey{keyPair.Public})
	err = testUser.Install(userName, context.Directory)
	if err != nil && !strings.Contains(err.Error(), "already ") {
		return err
	}
	return nil
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
