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
	"upspin.googlesource.com/upspin.git/path"
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

// Setup is a configuration structure that contains a directory tree and other optional flags.
type Setup struct {
	IgnoreExistingDirectories bool
	Tree                      Tree
}

// Tree is a full directory tree with path names and their contents.
type Tree []Entry

// MakeTree creates a new directory tree for the owner as per the setup using the given client.
func MakeTree(client upspin.Client, ownerName upspin.UserName, setup Setup) error {
	if client == nil {
		return errors.New("client must not be nil")
	}
	for _, e := range setup.Tree {
		if strings.HasSuffix(e.P, "/") {
			if len(e.C) > 0 {
				return errors.New("directory entry must not have contents")
			}
			dir := path.Join(upspin.PathName(ownerName), e.P)
			loc, err := client.MakeDirectory(dir)
			if err != nil {
				if !setup.IgnoreExistingDirectories {
					log.Printf("Tree: Error creating directory %s: %s", dir, err)
					return err
				}
			}
			log.Printf("Tree: Created dir %s at %v", dir, loc)
		} else {
			name := path.Join(upspin.PathName(ownerName), e.P)
			loc, err := client.Put(name, []byte(e.C))
			if err != nil {
				log.Printf("Error creating file %s: %s", name, err)
				return err
			}
			log.Printf("Tree: Created file %s at %v", name, loc)
		}
	}
	log.Printf("Tree: All entries created.")
	return nil
}

// N (short for New) is a helper function to return a new Entry.
func N(pathName string, contents string) Entry {
	return Entry{
		P: pathName,
		C: contents,
	}
}

// GCP returns a Client pointing to the GCP test instances on upspin.io given a Context partially initialized
// with a user and keys.
func GCP(context *upspin.Context) (upspin.Client, error) {
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
	err := bindEndpoints(context, endpointStore, endpointDir, endpointUser)
	if err != nil {
		return nil, err
	}
	client := client.New(context)
	return client, nil
}

// InProcess returns a Client pointing to in-process instances given a Context partially initialized
// with a user and keys.
func InProcess(context *upspin.Context) (upspin.Client, error) {
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
	err := bindEndpoints(context, endpointStore, endpointDir, endpointUser)
	if err != nil {
		return nil, err
	}
	client := client.New(context)
	return client, nil
}

// NewContextForUser adds a new user to the testuser service, creates a new key for the user based on
// the chosen packing type and returns a partially filled Context.
func NewContextForUser(userName upspin.UserName, packing upspin.Packing) (*upspin.Context, error) {
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
		return nil, err
	}
	return NewContextForUserWithKey(userName, keyPair, packing)
}

// NewContextForUserWithKey adds a new user to the testuser service and returns a Context partially filled with user,
// key and packing type as given.
func NewContextForUserWithKey(userName upspin.UserName, keyPair *upspin.KeyPair, packing upspin.Packing) (*upspin.Context, error) {
	context := &upspin.Context{
		UserName: userName,
		Packing:  packing,
		KeyPair:  *keyPair,
	}

	endpointInProcess := upspin.Endpoint{
		Transport: upspin.InProcess,
		NetAddr:   "",
	}
	user, err := bind.User(context, endpointInProcess)
	if err != nil {
		return nil, err
	}
	testUser, ok := user.(*testuser.Service)
	if !ok {
		return nil, errors.New("user service must be the in-process instance")
	}
	// Set the public key for the registered user.
	testUser.SetPublicKeys(userName, []upspin.PublicKey{keyPair.Public})
	return context, nil
}

// InstallUserRoot makes a root for the user in the context.
func InstallUserRoot(context *upspin.Context) error {
	testUser, ok := context.User.(*testuser.Service)
	if !ok {
		return errors.New("user service must be the in-process instance")
	}
	testUser.AddRoot(context.UserName, context.Directory.Endpoint())
	// Make the root to be sure it's there.
	_, err := context.Directory.MakeDirectory(upspin.PathName(context.UserName + "/"))
	if err != nil && !strings.Contains(err.Error(), "already ") {
		return err
	}
	return nil
}

func bindEndpoints(context *upspin.Context, store, dir, user upspin.Endpoint) error {
	var err error
	context.Store, err = bind.Store(context, store)
	if err != nil {
		return err
	}
	context.Directory, err = bind.Directory(context, dir)
	if err != nil {
		return err
	}
	context.User, err = bind.User(context, user)
	if err != nil {
		return err
	}
	return nil
}
