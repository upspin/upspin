// Package gcpdir implements the interface upspin.Directory for talking to an HTTP server.
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
	goPath "path"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	serverError = "server error: %v"
)

// Directory is an implementation of upspin.Directory that uses GCP to store its data.
type Directory struct {
	serverURL    string
	storeService upspin.Store
	client       netutil.HTTPClientInterface
	timeNow      func() upspin.Time
}

// Guarantee we implement the interface
var _ upspin.Directory = (*Directory)(nil)

var (
	zeroLoc upspin.Location
)

// newDirectory returns a concrete implementation of Directory, pointing to a server at a given URL and port.
func newDirectory(serverURL string, storeService upspin.Store, client netutil.HTTPClientInterface, timeFunc func() upspin.Time) *Directory {
	if timeFunc == nil {
		timeFunc = upspin.Now
	}
	return &Directory{
		serverURL:    serverURL,
		storeService: storeService,
		client:       client,
		timeNow:      timeFunc,
	}
}

// Lookup implements Directory.
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

// Put implements Directory.
func (d *Directory) Put(name upspin.PathName, data []byte, packdata upspin.PackData, opts *upspin.PutOptions) (upspin.Location, error) {
	const op = "Put"

	if len(packdata) < 1 {
		return zeroLoc, newError(op, name, errors.New("missing packing type in packdata"))
	}
	parsed, err := path.Parse(name)
	if err != nil {
		return zeroLoc, newError(op, name, errors.New("invalid path"))
	}
	canonicalName := parsed.Path()

	// Now, let's make a directory entry to Put to the server
	var dirEntry *upspin.DirEntry

	// Check whether this is an Access file, which is special and must be handled here, since the dir
	// server does not see file contents.
	if access.IsAccessFile(canonicalName) {
		if upspin.Packing(packdata[0]) != upspin.PlainPack {
			// The directory service must be able to read the bytes passed in.
			return zeroLoc, newError(op, canonicalName, errors.New("packing must be plain for Access file"))
		}
		accessPerms, err := access.Parse(canonicalName, data)
		if err != nil {
			return zeroLoc, newError(op, "", err) // err already contains canonicalName
		}
		// TODO: no support for groups yet.
		readers := make([]upspin.UserName, 0, len(accessPerms.Readers))
		for _, r := range accessPerms.Readers {
			readers = append(readers, r.User)
		}

		// Patch the parent dir with updated readers.
		patchDirEntry := upspin.DirEntry{
			Name: parsed.Drop(1).Path(),
			Metadata: upspin.Metadata{
				Readers: readers,
			},
		}
		err = d.storeDirEntry(op, netutil.Patch, &patchDirEntry)
		if err != nil {
			return zeroLoc, err
		}
	}

	// Prepare default optionals.
	commitOpts := &upspin.PutOptions{
		Sequence: 0, // Explicit for clarity. 0 = don't care.
		Size:     uint64(len(data)),
		Time:     d.timeNow(),
	}
	if opts != nil {
		if opts.Size != 0 {
			commitOpts.Size = opts.Size
		}
		if opts.Time != 0 {
			commitOpts.Time = opts.Time
		}
		if opts.Sequence != 0 {
			commitOpts.Sequence = opts.Sequence
		}
	}

	// First, store the data itself, to find the reference.
	// TODO: bind to the Store server pointed at by the dirEntry instead of using the default one.
	ref, err := d.storeService.Put(data)
	if err != nil {
		log.Printf("storeService returned error: %v", err)
		return zeroLoc, err
	}
	// We now have a final Reference in ref. We now create a
	// directory entry for this Location.  From here on, if an
	// error occurs, we'll have a dangling block. We could delete
	// it, but we can always do fsck-style operations to find them
	// later.
	dirEntry = &upspin.DirEntry{
		Name: canonicalName,
		Location: upspin.Location{
			Reference: ref,
			Endpoint:  d.storeService.Endpoint(),
		},
		Metadata: upspin.Metadata{
			IsDir:    false,
			Sequence: commitOpts.Sequence,
			Size:     commitOpts.Size,
			Time:     commitOpts.Time,
			PackData: packdata,
			Readers:  nil, // Server will update.
		},
	}
	err = d.storeDirEntry(op, netutil.Post, dirEntry)
	if err != nil {
		return zeroLoc, err
	}

	return dirEntry.Location, nil
}

// storeDirEntry stores the given dirEntry in the server by applying an HTTP method (POST or PATCH accepted by server).
func (d *Directory) storeDirEntry(op string, HTTPMethod string, dirEntry *upspin.DirEntry) error {
	name := dirEntry.Name
	// Encode dirEntry as JSON
	dirEntryJSON, err := json.Marshal(dirEntry)
	if err != nil {
		return newError(op, name, err)
	}

	// Prepare a put request to the server
	req, err := http.NewRequest(HTTPMethod, fmt.Sprintf("%s/put", d.serverURL), bytes.NewBuffer(dirEntryJSON))
	if err != nil {
		return newError(op, name, err)
	}
	respBody, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return err
	}
	err = parser.ErrorResponse(respBody)
	if err != nil {
		return newError(op, name, err)
	}

	return nil
}

// MakeDirectory implements Directory.
func (d *Directory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	const op = "MakeDirectory"

	parsed, err := path.Parse(dirName)
	if err != nil {
		return zeroLoc, err
	}
	// Unless this is the root dir, we do a lookup to find the parent, so we can inherit Endpoint.
	parentEndpoint := upspin.Endpoint{ // Default endpoint, if parent does not have one.
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(d.serverURL),
	}
	if !parsed.IsRoot() {
		parentDirEntry, err := d.Lookup(parsed.Drop(1).Path())
		if err != nil {
			return zeroLoc, err
		}
		parentEndpoint = parentDirEntry.Location.Endpoint
	}

	// Prepares a request to put dirName to the server
	dirEntry := upspin.DirEntry{
		Name: parsed.Path(),
		Location: upspin.Location{
			// Reference is ignored.
			// Endpoint for dir entries is where the Directory server is.
			Endpoint: parentEndpoint,
		},
		Metadata: upspin.Metadata{
			IsDir:    true,
			Sequence: 0, // don't care?
			Size:     0, // Being explicit that dir entries have zero size.
			Time:     d.timeNow(),
			PackData: nil,
			Readers:  nil, // Must be nil, server will fill in.
		},
	}
	// TODO: dial the endpoint as listed in dirEntry and store it there instead.
	err = d.storeDirEntry(op, netutil.Post, &dirEntry)
	if err != nil {
		return zeroLoc, err
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
	depth := len(parsed.Elems) - clear
	dirs := struct{ Names []string }{}
	if depth > 0 {
		log.Printf("Globbing prefix %v with pattern %v, depth %d\n", prefix, pattern, depth)

		// Issue request
		req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/list?prefix=%s&depth=%d", d.serverURL, prefix, depth), nil)
		if err != nil {
			return nil, err
		}
		body, err := d.requestAndReadResponseBody(op, pathPattern, req)
		if err != nil {
			return nil, err
		}
		// Interpret bytes as an annonymous JSON struct
		err = json.Unmarshal(body, &dirs)
		if err != nil {
			return nil, err
		}
	} else {
		// There is no depth, meaning we're trying to Glob a single file. Just look it up then.
		dirs.Names = []string{prefix}
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
		return nil, newError(op, path, fmt.Errorf(serverError, resp.StatusCode))
	}
	// Check the content type
	answerType := resp.Header.Get(netutil.ContentType)
	if !strings.HasPrefix(answerType, "application/json") {
		return nil, newError(op, path, fmt.Errorf("invalid response format: %v", answerType))
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

// Dial implements Dialer.
func (d *Directory) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	const op = "Dial"
	if context == nil {
		return nil, newError(op, "", errors.New("nil Context"))
	}
	serverURL, err := url.Parse(string(e.NetAddr))
	if err != nil {
		return nil, newError(op, "", fmt.Errorf("required endpoint with a valid HTTP address: %v", err))
	}
	d.serverURL = serverURL.String()
	d.storeService = context.Store
	authClient, isSecure := d.client.(*auth.HTTPClient)
	if isSecure {
		authClient.SetUserName(context.UserName)
		authClient.SetUserKeys(context.KeyPair)
	}
	return d, nil
}

// ServerUserName implements Dialer.
func (d *Directory) ServerUserName() string {
	return "GCP Directory"
}

type dirError struct {
	op   string
	path upspin.PathName
	err  error
}

func (e *dirError) Error() string {
	if e.path == "" {
		return fmt.Sprintf("Directory: %s: %s", e.op, e.err)
	}
	return fmt.Sprintf("Directory: %s: %s: %s", e.op, e.path, e.err)
}

func newError(op string, path upspin.PathName, err error) *dirError {
	return &dirError{
		op:   op,
		path: path,
		err:  err,
	}
}

func init() {
	// By default, set up only the HTTP client. Everything else gets bound at Dial time.
	bind.RegisterDirectory(upspin.GCP, newDirectory("", nil, auth.NewAnonymousClient(&http.Client{}), nil))
}
