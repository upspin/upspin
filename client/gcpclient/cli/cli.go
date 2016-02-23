// Simple utility for reading/writing files on GCP using the Unsafe packing.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"

	client "upspin.googlesource.com/upspin.git/client/gcpclient"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	dirLocation   = flag.String("directory", "http://localhost:8081", "URL of the directory service location")
	storeLocation = flag.String("store", "http://localhost:8080", "URL of the store service location")
	inFile        = flag.String("in", "", "full pathname of file to be Put or empty for stdin")
	outFile       = flag.String("out", "", "output file")
	c             = newClient()
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
	case "get":
		get(path)
	default: // TODO: ls
		fmt.Fprintf(os.Stderr, "Can't understand command: %v\n", flag.Arg(0))
		Usage()
	}
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\tcli [flags] <mkdir|put|get> <path>\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func mkdir(pathName upspin.PathName) {
	loc, err := c.MakeDirectory(pathName)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("mkdir succeeded: %v+\n", loc)
}

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
	loc, err := c.Put(pathName, data)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Put: %+v", loc)
}

func get(pathName upspin.PathName) {
	data, err := c.Get(pathName)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Success reading file. Len: %d", len(data))
	// Write to outfile or to stdout if none set
	var output *os.File
	if *outFile == "" {
		output = os.Stdout
	} else {
		output, err = os.Create(*outFile)
		if err != nil {
			log.Fatal(err)
		}
		defer output.Close()
	}
	_, err = io.Copy(output, bytes.NewReader(data))
	if err != nil {
		log.Fatalf("Copying to output failed: %v", err)
	}
}

func newClient() upspin.Client {
	// Pre-load some keys into the system.
	userKeys := []client.UserKeys{
		client.UserKeys{
			User:   upspin.UserName("edpin@google.com"),
			Public: upspin.PublicKey("Zee Kee"),
		},
		client.UserKeys{
			User:   upspin.UserName("p@google.com"),
			Public: upspin.PublicKey("p's key"),
		},
		client.UserKeys{
			User:   upspin.UserName("ehg@google.com"),
			Public: upspin.PublicKey("Captain Crypto"),
		},
		client.UserKeys{
			User:   upspin.UserName("r@google.com"),
			Public: upspin.PublicKey("Commander Pike"),
		},
	}
	return client.NewForTesting(*storeLocation, *dirLocation, userKeys)
}
