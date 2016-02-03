package main

// Simple client for using the Store interface on the command line.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	store "upspin.googlesource.com/upspin.git/store/gcp"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	Store upspin.Store = store.New("http://localhost:8080", &http.Client{})

	inFile  = flag.String("in", "", "input file")
	outFile = flag.String("out", "", "output file")
)

func main() {
	flag.Usage = Usage
	flag.Parse()

	if len(flag.Args()) != 2 {
		Usage()
	}

	switch strings.ToLower(flag.Arg(0)) {
	case "get":
		get(flag.Arg(1))
	case "put":
		put(flag.Arg(1))
	default:
		log.Println("Can't understand command. Use GET or PUT")
		Usage()
	}
}

func Usage() {
	fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\tcli [flags] <GET|PUT> <ref>\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func get(refStr string) {
	ref := upspin.Reference{
		Key:     refStr,
		Packing: upspin.EndToEnd,
	}
	loc := upspin.Location{
		Reference: ref,
	}

	buf, _, err := innerGet(loc, 0)

	if err != nil {
		log.Fatal(err)
	}
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
	_, err = io.Copy(output, bytes.NewReader(buf))
	if err != nil {
		log.Fatal("Copying to output failed: %v", err)
	}
}

func innerGet(loc upspin.Location, count int) ([]byte, []upspin.Location, error) {
	if count > 3 {
		return nil, nil, errors.New("Too many redirections")
	}
	buf, locs, err := Store.Get(loc)
	if err != nil {
		log.Fatalf("Error getting from server: %v", err)
	}
	if locs != nil {
		log.Println("We got redirected. Following new location: %v", locs[0])
		buf, locs, err = innerGet(locs[0], count+1)
	}
	return buf, locs, err
}

func put(refStr string) {
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

	ref := upspin.Reference{
		Key:     refStr,
		Packing: upspin.EndToEnd,
	}

	loc, err := Store.Put(ref, data)
	if err != nil {
		log.Fatalf("Error putting to server: %v", err)
	}
	log.Printf("Put file to storage. Location: %v", loc)
}
