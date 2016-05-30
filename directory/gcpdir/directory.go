// Package gcpdir implements the interface upspin.Directory for talking to an HTTP server.
package gcpdir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	goPath "path"
	"strings"

	"upspin.io/access"
	"upspin.io/auth/httpauth"
	"upspin.io/bind"
	"upspin.io/cloud/netutil"
	"upspin.io/cloud/netutil/jsonmsg"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	serverError = "server error: %v"
)

// Directory is an implementation of upspin.Directory that uses GCP to store its data.
type Directory struct {
	upspin.NoConfiguration
	endpoint  upspin.Endpoint
	serverURL string
	client    netutil.HTTPClientInterface
	timeNow   func() upspin.Time
}

// Guarantee we implement the interface
var _ upspin.Directory = (*Directory)(nil)

var zeroLoc upspin.Location

// newDirectory returns a concrete implementation of Directory, pointing to a server at a given URL and port.
func newDirectory(serverURL string, client netutil.HTTPClientInterface, timeFunc func() upspin.Time) *Directory {
	if timeFunc == nil {
		timeFunc = upspin.Now
	}
	return &Directory{
		serverURL: serverURL,
		client:    client,
		timeNow:   timeFunc,
	}
}

// Lookup implements Directory.
func (d *Directory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	const op = "Lookup"
	// Prepare a get request to the server
	req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/dir/%s", d.serverURL, name), nil)
	if err != nil {
		return nil, newError(op, name, err)
	}
	body, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return nil, err
	}
	// Interpret the JSON returned
	dirEntry, err := jsonmsg.DirEntryResponse(body)
	if err != nil {
		return nil, err
	}
	return dirEntry, nil
}

// Put implements Directory.
func (d *Directory) Put(dirEntry *upspin.DirEntry) error {
	const op = "Put"

	name := dirEntry.Name
	if len(dirEntry.Metadata.Packdata) < 1 {
		return newError(op, name, errors.New("missing packing type in packdata"))
	}
	parsed, err := path.Parse(name)
	if err != nil {
		return newError(op, name, errors.New("invalid path"))
	}
	canonicalName := parsed.Path()

	if access.IsAccessFile(canonicalName) && upspin.Packing(dirEntry.Metadata.Packdata[0]) != upspin.PlainPack {
		// The directory server must be able to read the bytes from the reference.
		return newError(op, canonicalName, errors.New("packing must be plain for Access file"))
	}

	dirEntry.Name = canonicalName

	// Now, Put to the server.
	err = d.storeDirEntry(op, netutil.Post, dirEntry)
	if err != nil {
		return err
	}

	return nil
}

// WhichAccess implements Directory.
func (d *Directory) WhichAccess(name upspin.PathName) (upspin.PathName, error) {
	const op = "WhichAccess"
	// Prepare a get request to the server
	req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/whichaccess/%s", d.serverURL, name), nil)
	if err != nil {
		return "", newError(op, name, err)
	}
	body, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return "", err
	}
	// Interpret the JSON returned
	acc, err := jsonmsg.WhichAccessResponse(body)
	if err != nil {
		return "", err
	}
	return acc, nil

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
	req, err := http.NewRequest(HTTPMethod, fmt.Sprintf("%s/dir/%s", d.serverURL, dirEntry.Name), bytes.NewBuffer(dirEntryJSON))
	if err != nil {
		return newError(op, name, err)
	}
	respBody, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return err
	}
	err = jsonmsg.ErrorResponse(respBody)
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
			Attr:     upspin.AttrDirectory,
			Sequence: 0, // don't care?
			Size:     0, // Being explicit that dir entries have zero size.
			Time:     d.timeNow(),
			Packdata: nil,
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

	// Issue request
	req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/glob/%s", d.serverURL, parsed), nil)
	if err != nil {
		return nil, err
	}
	body, err := d.requestAndReadResponseBody(op, pathPattern, req)
	if err != nil {
		return nil, err
	}
	// Interpret bytes as an annonymous JSON struct
	var dirs []upspin.DirEntry
	err = json.Unmarshal(body, &dirs)
	if err != nil {
		return nil, err
	}
	dirPtrs := make([]*upspin.DirEntry, len(dirs))
	for i := 0; i < len(dirs); i++ {
		dirPtrs[i] = &dirs[i]
	}
	return dirPtrs, nil
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
func (d *Directory) Dial(context *upspin.Context, e upspin.Endpoint) (upspin.Service, error) {
	const op = "Dial"
	if context == nil {
		return nil, newError(op, "", errors.New("nil Context"))
	}
	serverURL, err := url.Parse(string(e.NetAddr))
	if err != nil {
		return nil, newError(op, "", fmt.Errorf("required endpoint with a valid HTTP address: %v", err))
	}
	// Checking for reachability is done on the default HTTP client, so no new allocations happen here.
	if !netutil.IsServerReachable(serverURL.String()) {
		return nil, newError(op, "", fmt.Errorf("Directory server unreachable"))
	}
	// Need to create a new instance.
	log.Debug.Printf("Dial: Creating a new instance for endpoint %v and context %v", e, context)
	dir := &Directory{
		endpoint:  e,
		serverURL: serverURL.String(),
		timeNow:   d.timeNow,
		client:    httpauth.NewClient(context.UserName, context.Factotum, &http.Client{}),
	}
	return dir, nil
}

// Ping implements Service.
func (d *Directory) Ping() bool {
	return netutil.IsServerReachable(d.serverURL)
}

// Close implements upspin.Service.
func (d *Directory) Close() {
	// TODO
}

// Authenticate implements upspin.Service.
func (d *Directory) Authenticate(*upspin.Context) error {
	// TODO
	return nil
}

// Delete deletes the DirEntry for a name from the backend.
func (d *Directory) Delete(name upspin.PathName) error {
	const op = "Delete"
	// Prepare a get request to the server
	req, err := http.NewRequest(netutil.Delete, fmt.Sprintf("%s/dir/%s", d.serverURL, name), nil)
	if err != nil {
		return newError(op, name, err)
	}
	body, err := d.requestAndReadResponseBody(op, name, req)
	if err != nil {
		return err
	}
	// Interpret the JSON returned
	return jsonmsg.ErrorResponse(body)
}

// Endpoint implements upspin.Directory.Endpoint.
func (d *Directory) Endpoint() upspin.Endpoint {
	return d.endpoint
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
	bind.RegisterDirectory(upspin.GCP, newDirectory("", httpauth.NewAnonymousClient(&http.Client{}), nil))
}
