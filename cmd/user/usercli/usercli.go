// Simple command line utility for using a GCP User interface on the command line.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/upspin"

	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

var (
	userLocation = flag.String("user", "https://upspin.io:8082", "URL of the user service location")
	keyFile      = flag.String("key", "", "full pathname of the public key file used with addkey or empty for stdin")
	endpointLoc  = flag.String("endpoint", "", "and endpoint for adding a root. This consists of the value of the Transport type optionally followed by a comma and the NetAddr of the endpoint. (e.g. for a local GCP use -endpoint=gcp,http://localhost:8081)")
)

func main() {
	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) != 2 {
		usage()
	}

	name := upspin.UserName(flag.Arg(1))
	switch strings.ToLower(flag.Arg(0)) {
	case "lookup":
		lookup(name)
	case "addroot":
		addRoot(name, *endpointLoc)
	case "addkey":
		addKey(name)
	default:
		fmt.Fprintf(os.Stderr, "Can't understand command: %v\n", flag.Arg(0))
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\tcli [flags] <lookup|addroot|addkey> <user>\n")
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
	fmt.Println("Roots:")
	for i, r := range roots {
		fmt.Printf("%d: %+v\n", i, r)
	}
	fmt.Println("Keys:")
	for i, k := range keys {
		fmt.Printf("%d: %s\n", i, k)
	}
}

func addRoot(user upspin.UserName, endpointStr string) {
	if endpointStr == "" {
		fmt.Fprintf(os.Stderr, "No endpoint specified for user.")
		usage()
	}
	e, err := endpoint.Parse(endpointStr)
	if err != nil {
		log.Fatal(err)
	}
	endpointJSON, err := json.Marshal(e)
	if err != nil {
		log.Fatal(err)
	}

	// This requires that we talk to the user server directly,
	// without using the User gcp client.
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/addroot?user=%s&endpoint=%s", *userLocation, user, url.QueryEscape(string(endpointJSON))), nil)
	if err != nil {
		log.Fatal(err)
	}
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	err = parser.ErrorResponse(body)
	if err != nil {
		log.Fatalf("Server replied with error: %s", err)
	}
	fmt.Println("Root added successfully.")
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
			log.Fatalf("Key file must contain the public key. Filename %q is not accepted.", *keyFile)
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
	u, err := bind.User(context, e)
	if err != nil {
		log.Fatalf("Can't bind to Directory: %v", err)
	}
	return u
}
