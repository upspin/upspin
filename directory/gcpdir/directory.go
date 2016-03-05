// Package directory implements the interface upspin.Directory for talking to an HTTP server.
package gcpdir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	goPath "path"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	serverError       = "server error: %v"
	accessControlFile = "Access" // TODO: this should be global (in upspin.go?)
)

// Directory is an implementation of upspin.Directory that uses GCP to store its data.
type Directory struct {
	serverURL    string
	storeService upspin.Store
	client       netutil.HTTPClientInterface
}

// Guarantee we implement the interface
var _ upspin.Directory = (*Directory)(nil)

// new returns a concrete implementation of Directory, pointing to a server at a given URL and port.
func new(serverURL string, storeService upspin.Store, client netutil.HTTPClientInterface) *Directory {
	return &Directory{
		serverURL:    serverURL,
		storeService: storeService,
		client:       client,
	}
}

func (d *Directory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "Lookup"
	// Prepare a get request to the server
	req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/get?pathname=%s", d.serverURL, name), nil)
	if err != nil {
		return nil, newError(op, name, err)
	}
	body, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return nil, err
	}
	// Interpret the JSON returned
	dirEntry, err := parser.DirEntryResponse(body)
	if err != nil {
		return nil, err
	}
	return dirEntry, nil
}

// getReaders parses the contents of data and interprets each line as an upspin.UserName.
// TODO: handle groups, as full pathnames (e.g. foo@bar.com/Group/mygroup.txt)
func getReaders(data []byte) ([]upspin.UserName, error) {
	names := strings.Split(string(data), "\n")
	readers := make([]upspin.UserName, 0, len(names))
	var errCnt int
	for _, n := range names {
		if n == "" {
			// Skip empty lines
			continue
		}
		p, err := path.Parse(upspin.PathName(n) + "/")
		if err != nil {
			log.Printf("not an Upspin user name: %s", n)
			errCnt++
		}
		log.Printf("Adding user %s as a reader", p.User)
		readers = append(readers, p.User)
	}
	var err error
	if len(readers) == 0 {
		err = errors.New("access: no valid Upspin user name")
	}
	if errCnt > 0 {
		err = errors.New("access: some user names are not valid")
	}
	return readers, err
}

func (d *Directory) Put(name upspin.PathName, data []byte, packdata upspin.PackData) (upspin.Location, error) {
	var zeroLoc upspin.Location
	const op = "Put"

	// Check whether this is an Access file, which is special.
	parsed, err := path.Parse(name)
	if err != nil {
		return zeroLoc, err
	}
	// Directory entry to put to the server
	var dirEntry *upspin.DirEntry
	if len(parsed.Elems) > 0 && parsed.Elems[len(parsed.Elems)-1] == accessControlFile {
		// This is special file. Update its directory entry accordingly.
		dirEntry, err = d.Lookup(parsed.Drop(1).Path())
		if err != nil {
			return zeroLoc, err
		}
		readers, err := getReaders(data)
		if err != nil {
			return zeroLoc, err
		}
		// Completely replaces previous Readers.
		// TODO: no support for groups yet.
		dirEntry.Metadata.Readers = readers
	} else {
		if len(packdata) < 1 {
			return zeroLoc, newError(op, name, errors.New("missing packing type in packdata"))
		}

		// First, store the data itself, to find the key
		key, err := d.storeService.Put(data)
		if err != nil {
			log.Printf("storeService returned error: %v", err)
			return zeroLoc, err
		}
		// We now have a final Location in loc. We now create a
		// directory entry for this Location.  From here on, if an
		// error occurs, we'll have a dangling block. We could delete
		// it, but we can always do fsck-style operations to find them
		// later.
		dirEntry = &upspin.DirEntry{
			Name: name,
			Location: upspin.Location{
				Reference: upspin.Reference{
					Key:     key,
					Packing: upspin.Packing(packdata[0]),
				},
				Endpoint: d.storeService.Endpoint(),
			},
			Metadata: upspin.Metadata{
				IsDir:    false,
				Sequence: 0, // TODO: server does not increment currently
				PackData: packdata,
			},
		}
	}

	// Encode dirEntry as JSON
	dirEntryJSON, err := json.Marshal(dirEntry)
	if err != nil {
		return zeroLoc, newError(op, name, err)
	}

	// Prepare a put request to the server
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", d.serverURL), bytes.NewBuffer(dirEntryJSON))
	if err != nil {
		return zeroLoc, newError(op, name, err)
	}
	respBody, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return zeroLoc, err
	}
	err = parser.ErrorResponse(respBody)
	if err != nil {
		return zeroLoc, newError(op, name, err)
	}

	return dirEntry.Location, nil
}

func (d *Directory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	var zeroLoc upspin.Location // The zero location
	const op = "MakeDirectory"

	// Prepares a request to put dirName to the server
	dirEntry := upspin.DirEntry{
		Name: dirName,
		Location: upspin.Location{
			// Key is ignored.
			// Endpoint is where the server is.
			Endpoint: upspin.Endpoint{
				Transport: upspin.GCP,
				NetAddr:   upspin.NetAddr(d.serverURL),
			},
		},
		Metadata: upspin.Metadata{
			IsDir:    true,
			Sequence: 0, // don't care?
			PackData: nil,
		},
	}
	body, err := json.Marshal(dirEntry)
	if err != nil {
		return zeroLoc, newError(op, dirName, err)
	}
	data := bytes.NewBuffer(body)
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", d.serverURL), data)
	if err != nil {
		return zeroLoc, newError(op, dirName, err)
	}
	respBody, err := d.requestAndReadResponseBody(op, dirName, req)
	if err != nil {
		return zeroLoc, err
	}
	err = parser.ErrorResponse(respBody)
	if err != nil {
		return zeroLoc, newError(op, dirName, err)
	}

	return dirEntry.Location, nil
}

// Glob traverses the directory structure looking for entries that
// match a given pattern. The pattern must be a full pathname,
// including the user name which may not contain metacharacters (they
// will be treated literally).
func (d *Directory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	op := "Glob"
	// Check if pattern is a valid upspin path
	pathPattern := upspin.PathName(pattern)
	parsed, err := path.Parse(pathPattern)
	if err != nil {
		return nil, newError(op, pathPattern, err)
	}
	// Check if pattern is a valid go path pattern
	_, err = goPath.Match(parsed.FilePath(), "")
	if err != nil {
		return nil, newError(op, pathPattern, err)
	}

	// As an optimization, we look for the longest prefix that
	// does not contain a metacharacter -- this saves us from
	// doing a full list operation if the matter of interest is
	// deep in a sub directory.
	clear := len(parsed.Elems)
	for i, elem := range parsed.Elems {
		if strings.ContainsAny(elem, "*?[]^") {
			clear = i
			break
		}
	}
	prefix := parsed.First(clear).String()
	log.Printf("Globbing prefix %v with pattern %v\n", prefix, pattern)

	// Issue request
	req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/list?prefix=%s", d.serverURL, prefix), nil)
	if err != nil {
		return nil, err
	}
	body, err := d.requestAndReadResponseBody(op, pathPattern, req)
	if err != nil {
		return nil, err
	}
	// Interpret bytes as an annonymous JSON struct
	dirs := &struct{ Names []string }{}
	err = json.Unmarshal(body, dirs)
	if err != nil {
		return nil, err
	}
	log.Printf("To be globbed: %v", dirs)

	dirEntries := make([]*upspin.DirEntry, 0, len(dirs.Names))
	// Now do the actual globbing.
	var firstError error
	for _, path := range dirs.Names {
		// error is ignored as pattern is known valid
		if match, _ := goPath.Match(pattern, path); match {
			// Now fetch each DirEntry we need
			log.Printf("Looking up: %v", path)
			// TODO: should we include metadata?
			de, err := d.Lookup(upspin.PathName(path))
			if err != nil {
				// Save the error but keep going
				if firstError == nil {
					firstError = err
				}
				continue
			}
			dirEntries = append(dirEntries, de)
		}
	}

	return dirEntries, firstError
}

// requestAndReadResponseBody is an internal helper function that
// sends a given request over the HTTP client and parses the body of
// the reply, using op and path to build errors if they are
// encountered along the way.
func (d *Directory) requestAndReadResponseBody(op string, path upspin.PathName, req *http.Request) ([]byte, error) {
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, newError(op, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newError(op, path, errors.New(fmt.Sprintf(serverError, resp.StatusCode)))
	}
	// Check the content type
	answerType := resp.Header.Get(netutil.ContentType)
	if !strings.HasPrefix(answerType, "application/json") {
		return nil, newError(op, path, errors.New(fmt.Sprintf("invalid response format: %v", answerType)))
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, newError(op, path, err)
	}

	return respBody, nil
}

func (d *Directory) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	const op = "Dial"
	if context == nil {
		return nil, newError(op, "", errors.New("nil Context"))
	}
	serverURL, err := url.Parse(string(e.NetAddr))
	if err != nil {
		return nil, newError(op, "", errors.New(fmt.Sprintf("required endpoint with a valid HTTP address: %v", err)))
	}
	d.serverURL = serverURL.String()
	d.storeService = context.Store
	return d, nil
}

func (d *Directory) ServerUserName() string {
	return "GCP Directory"
}

func newError(op string, path upspin.PathName, err error) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: string(path),
		Err:  err,
	}
}

func init() {
	// By default, set up only the HTTP client. Everything else gets bound at Dial time.
	bind.RegisterDirectory(upspin.GCP, new("", nil, &http.Client{}))
}
