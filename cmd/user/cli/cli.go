// Simple command line utility for using a GCP User interface on the command line.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

var (
	userLocation = flag.String("user", "http://localhost:8082", "URL of the user service location")
	keyFile      = flag.String("key", "", "full pathname of the PUBLIC key file used with addkey or empty for stdin")
)

func main() {
	flag.Usage = Usage
	flag.Parse()

	if len(flag.Args()) != 2 {
		Usage()
	}

	name := upspin.UserName(flag.Arg(1))
	switch strings.ToLower(flag.Arg(0)) {
	case "lookup":
		lookup(name)
	case "setroot":
		setRoot(name)
	case "addkey":
		addKey(name)
	default:
		fmt.Fprintf(os.Stderr, "Can't understand command: %v\n", flag.Arg(0))
		Usage()
	}
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\tcli [flags] <lookup|setroot|addkey> <user>\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func lookup(user upspin.UserName) {
	u := newUser()
	roots, keys, err := u.Lookup(user)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("roots: %+v\n", roots)
	fmt.Printf("keys: %+v\n", keys)
}

func setRoot(user upspin.UserName) {
	// This requires that we talk to the user server directly,
	// without using the User gcp client.

	// First, we need to read all information about a user because
	// we're going to delete the user and re-create it with a new
	// root. This CLI does not currently support adding new roots,
	// only setting one -- we want to avoid repeating the same
	// root many times, which the server does not yet do for us.

	fmt.Fprintf(os.Stderr, "setRoot not implemented yet\n")
}

func addKey(user upspin.UserName) {
	// Read the new key from a file or from stdin.
	var input *os.File
	var err error
	if *keyFile == "" {
		input = os.Stdin
	} else {
		file := strings.ToLower(*keyFile)
		if strings.Contains(file, "secret") || strings.Contains(file, "private") {
			log.Fatalf("Key file must contain the PUBLIC key. Filename %q is not accepted.", *keyFile)
		}
		input, err = os.Open(*keyFile)
		if err != nil {
			log.Fatal(err)
		}
		defer input.Close()
	}
	data, err := ioutil.ReadAll(input)
	if err != nil {
		log.Fatal(err)
	}
	// Requires we talk to the user server directly,
	// without using the User gcp client.
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/addkey?user=%s&key=%s", *userLocation, user, url.QueryEscape(string(data))), nil)
	if err != nil {
		log.Fatal(err)
	}
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	log.Printf("%s", body)
}

func newUser() upspin.User {
	context := &upspin.Context{}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(*userLocation),
	}
	u, err := access.BindUser(context, e)
	if err != nil {
		log.Fatalf("Can't bind to Directory: %v", err)
	}
	return u
}
