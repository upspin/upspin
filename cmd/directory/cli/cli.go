package main

// Simple command line utility for using a GCP Directory interface on the command line.

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"log"

	"upspin.googlesource.com/upspin.git/access"
	dir "upspin.googlesource.com/upspin.git/directory/gcp"
	store "upspin.googlesource.com/upspin.git/store/gcp"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	d upspin.Directory = newDirectory(&http.Client{})

	dirLocation   = flag.String("directory", "http://localhost:8081", "URL of the directory service location")
	storeLocation = flag.String("store", "http://localhost:8080", "URL of the store service location")
	inFile        = flag.String("in", "", "full pathname of file to be Put or empty for stdin")
)

func main() {
	flag.Usage = Usage
	flag.Parse()

	if len(flag.Args()) != 2 {
		Usage()
	}

	path := upspin.PathName(flag.Arg(1))
	switch strings.ToLower(flag.Arg(0)) {
	case "mkdir":
		mkdir(path)
	case "put":
		put(path)
	case "lookup":
		lookup(path)
	default:
		fmt.Fprintf(os.Stderr, "Can't understand command: %v", flag.Arg(0))
		Usage()
	}
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\tcli [flags] <mkdir|put|lookup> <path>\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

// mkdir creates a new directory on GCP.
func mkdir(pathName upspin.PathName) {
	loc, err := d.MakeDirectory(pathName)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%+v", loc)
}

// put writes the contents of a file (given by flags or from stdin) to
// the given pathname on GCP.
func put(pathName upspin.PathName) {
	var input *os.File
	var err error
	if *inFile == "" {
		input = os.Stdin
	} else {
		input, err = os.Open(*inFile)
		if err != nil {
			log.Fatal(err)
		}
		defer input.Close()
	}
	data, err := ioutil.ReadAll(input)
	if err != nil {
		log.Fatal(err)
	}
	loc, err := d.Put(pathName, data, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%+v", loc)
}

// lookup retrieves the DirEntry from the Directory server on GCP.
func lookup(pathName upspin.PathName) {
	dirEntry, err := d.Lookup(pathName)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%+v", dirEntry)
}

// newStore creates a new upspin.Store client for talking to a GCP
// server, using an http.Client as transport.
func newStore(client *http.Client) upspin.Store {
	context := store.Context{
		ServerURL: *storeLocation,
		Client:    client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
	}
	s, err := access.BindStore(context, e)
	if err != nil {
		log.Fatalf("Can't bind to Store: %v", err)
	}
	return s
}

// newDirectory creates a new upspin.Directory client for talking to a GCP
// server, using an http.Client as transport.
func newDirectory(client *http.Client) upspin.Directory {
	context := dir.Context{
		ServerURL:    *dirLocation,
		StoreService: newStore(client),
		Client:       client,
	}
	e := upspin.Endpoint{
		Transport: upspin.GCP,
	}
	d, err := access.BindDirectory(context, e)
	if err != nil {
		log.Fatalf("Can't bind to Directory: %v", err)
	}
	return d
}
