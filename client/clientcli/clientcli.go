// Simple utility for exercising the client against the user's default context.
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

	"upspin.googlesource.com/upspin.git/client"
	"upspin.googlesource.com/upspin.git/context"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"

	// Load useful packers
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"

	// Load required gcp services
	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"
)

var (
	inFile     = flag.String("in", "", "full pathname of file to be Put or empty for stdin")
	outFile    = flag.String("out", "", "output file")
	longFormat = flag.Bool("l", false, "Enables long format for ls")
	c          = newClient()
)

func main() {
	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) != 2 {
		usage()
	}

	path := upspin.PathName(flag.Arg(1))
	switch strings.ToLower(flag.Arg(0)) {
	case "mkdir":
		mkdir(path)
	case "put":
		put(path)
	case "get":
		get(path)
	case "ls":
		ls(path)
	default:
		fmt.Fprintf(os.Stderr, "Can't understand command: %v\n", flag.Arg(0))
		usage()
	}
}

func usage() {
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

func ls(pathName upspin.PathName) {
	de, err := c.Glob(string(pathName))
	if err != nil {
		log.Fatal(err)
	}

	if *longFormat {
		printLongDirEntries(de)
	} else {
		printShortDirEntries(de)
	}
}

func printShortDirEntries(de []*upspin.DirEntry) {
	for _, e := range de {
		if e.Metadata.IsDir {
			fmt.Printf("%s/\n", e.Name)
		} else {
			fmt.Printf("%s\n", e.Name)
		}
	}
}

type domains map[string][]string // map[domain][]user

// addUser parses and adds a new user to the map of users and domains.
func (d domains) addUser(userName upspin.UserName) {
	user, domain, err := path.UserAndDomain(userName)
	if err != nil {
		log.Fatal(err)
	}
	users, found := d[domain]
	if !found {
		d[domain] = make([]string, 1, 5)
	}
	d[domain] = append(users, user)
}

// getUsersByDomain writes a list of users grouped by domain. If more than one user exists for given domain,
// the usernames are appended together separated by a comma, as in p,r,edpin@google.com bar,baz@foo.com.
func (d domains) writeUsersByDomain(buf *bytes.Buffer) {
	var doms []string
	for dom := range d {
		doms = append(doms, dom)
	}
	for j, dom := range doms {
		users := d[dom]
		for i, u := range users {
			buf.WriteString(u)
			if i < len(users)-1 {
				buf.WriteByte(',')
			}
		}
		buf.WriteByte('@')
		buf.WriteString(dom)
		if j < len(doms)-1 {
			buf.WriteByte(' ')
		}
	}
}

func formatReaders(readers []upspin.UserName) string {
	const (
		maxLen = 44
	)
	if len(readers) == 0 {
		return ""
	}
	domainMap := domains{}
	for _, r := range readers {
		domainMap.addUser(r)
	}
	var formatted bytes.Buffer
	domainMap.writeUsersByDomain(&formatted)

	if formatted.Len() >= maxLen-3 {
		formatted.Truncate(maxLen)
		formatted.WriteString("...")
	}
	return formatted.String()
}

func printLongDirEntries(de []*upspin.DirEntry) {
	for _, e := range de {
		isDirChar := '_'
		if e.Metadata.IsDir {
			isDirChar = 'd'
			n := len(e.Name)
			if e.Name[n-1:n] != "/" {
				e.Name = e.Name + "/"
			}
		}
		endpt := endpoint.String(&e.Location.Endpoint)
		fmt.Printf("%c %d %d %s [%s]\t[%s]\t%s\n", isDirChar, e.Metadata.Sequence, e.Metadata.Size, e.Metadata.Time,
			endpt, formatReaders(e.Metadata.Readers), e.Name)
	}
}

func newClient() upspin.Client {
	ctx, err := context.InitContext(nil)
	if err != nil {
		log.Fatal(err)
	}
	return client.New(ctx)
}
