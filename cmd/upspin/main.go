// Upspin is a simple utility for exercising the upspin client against the user's default context.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"upspin.googlesource.com/upspin.git/client"
	"upspin.googlesource.com/upspin.git/context"
	"upspin.googlesource.com/upspin.git/endpoint"
	"upspin.googlesource.com/upspin.git/upspin"

	// Load useful packers
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"

	// Load required gcp services
	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"

	// Load required test services
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/testuser"

	// Load required remote services
	_ "upspin.googlesource.com/upspin.git/directory/remote"
)

var (
	c, ctx = newClient()
)

func main() {
	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) < 1 {
		usage()
	}

	args := flag.Args()[1:]
	switch strings.ToLower(flag.Arg(0)) {
	case "mkdir":
		mkdir(args...)
	case "put":
		put(args...)
	case "get":
		get(args...)
	case "ls":
		ls(args...)
	case "rm":
		rm(args...)
	case "whichaccess":
		whichAccess(args...)
	default:
		fmt.Fprintf(os.Stderr, "Can't understand command: %v\n", flag.Arg(0))
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage of upspin:\n")
	fmt.Fprintf(os.Stderr, "\tupspin [flags] <mkdir|put|get|ls|rm|whichaccess> <path>\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func subUsage(fs *flag.FlagSet, msg string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, "Usage: %s\n", msg)
		// How many flags?
		n := 0
		fs.VisitAll(func(*flag.Flag) { n++ })
		if n > 0 {
			fmt.Fprintf(os.Stderr, "Flags:\n")
			fs.PrintDefaults()
		}
		os.Exit(2)
	}
}

func whichAccess(args ...string) {
	fs := flag.NewFlagSet("whichaccess", flag.ExitOnError)
	fs.Usage = subUsage(fs, "whichaccess path...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		acc, err := ctx.Directory.WhichAccess(upspin.PathName(fs.Arg(i)))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(acc)
	}
}

func rm(args ...string) {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	fs.Usage = subUsage(fs, "rm path...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		err := ctx.Directory.Delete(upspin.PathName(fs.Arg(i)))
		if err != nil {
			log.Fatal(err)
		}
	}
}

func mkdir(args ...string) {
	fs := flag.NewFlagSet("mkdir", flag.ExitOnError)
	fs.Usage = subUsage(fs, "mkdir directory...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
	}
	for i := 0; i < fs.NArg(); i++ {
		loc, err := c.MakeDirectory(upspin.PathName(fs.Arg(i)))
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%s: %+v", fs.Arg(i), loc)
	}
}

func put(args ...string) {
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	inFile := fs.String("in", "", "input file (default standard input)")
	fs.Usage = subUsage(fs, "put [-in=inputfile] path")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	var input *os.File
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
	loc, err := c.Put(upspin.PathName(fs.Arg(0)), data)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: %+v", fs.Arg(0), loc)
}

func get(args ...string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	outFile := fs.String("out", "", "output file (default standard output)")
	fs.Usage = subUsage(fs, "get [-out=outputfile] path")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	data, err := c.Get(upspin.PathName(fs.Arg(0)))
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
	_, err = output.Write(data)
	if err != nil {
		log.Fatalf("Copying to output failed: %v", err)
	}
}

func ls(args ...string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	longFormat := fs.Bool("l", false, "long format")
	fs.Usage = subUsage(fs, "ls [-l] path...")
	err := fs.Parse(args)
	if err != nil {
		log.Fatal(err)
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	for i := 0; i < fs.NArg(); i++ {
		de, err := c.Glob(fs.Arg(i))
		if err != nil {
			log.Fatal(err)
		}

		if *longFormat {
			printLongDirEntries(de)
		} else {
			printShortDirEntries(de)
		}
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

func printLongDirEntries(de []*upspin.DirEntry) {
	seqWidth := 2
	sizeWidth := 2
	for _, e := range de {
		s := fmt.Sprintf("%d", e.Metadata.Sequence)
		if seqWidth < len(s) {
			seqWidth = len(s)
		}
		s = fmt.Sprintf("%d", e.Metadata.Size)
		if sizeWidth < len(s) {
			sizeWidth = len(s)
		}
	}
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
		// TODO: print readers when we have them again.
		fmt.Printf("%c %*d %*d %s [%s]\t%s\n",
			isDirChar,
			seqWidth, e.Metadata.Sequence,
			sizeWidth, e.Metadata.Size,
			e.Metadata.Time.Go().Local().Format("Mon Jan _2 15:04:05"),
			endpt,
			e.Name)
	}
}

/*
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
*/

func newClient() (upspin.Client, *upspin.Context) {
	ctx, err := context.InitContext(nil)
	if err != nil {
		log.Fatal(err)
	}
	return client.New(ctx), ctx
}
