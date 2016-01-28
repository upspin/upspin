// Package directory implements the interface upspin.Directory for talking to an HTTP server.
package directory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Directory is an implementation of upspin.Directory that uses GCP to store its data.
type Directory struct {
	serverURL string
	client    HTTPClientInterface
}

// Guarantee we implement the interface
var _ upspin.Directory = (*Directory)(nil)

// HTTPClientInterface is a minimal HTTP client interface. An instance of
// http.Client suffices.
// TODO(edpin): this should move somewhere, probably cloud/netutil.
type HTTPClientInterface interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

// Creates a concrete implementation of Directory, pointing to a server at a given URL and port.
func New(serverURL string, client HTTPClientInterface) *Directory {
	return &Directory{
		serverURL: serverURL,
		client:    client,
	}
}

func (d *Directory) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	panic("not implemented")
}

func (d *Directory) Put(name upspin.PathName, data []byte) (upspin.Location, error) {
	panic("not implemented")
}

func (d *Directory) MakeDirectory(dirName upspin.PathName) (upspin.Location, error) {
	var zeroLoc upspin.Location // The zero location
	op := "mkdir"

	// Prepares a request to put dirName to the server
	parsed, err := path.Parse(dirName)
	if err != nil {
		return zeroLoc, NewError(op, string(dirName), err)
	}
	dirEntry := upspin.DirEntry{
		Name: dirName,
		Metadata: upspin.Metadata{
			IsDir:     true,
			Sequence:  0, // don't care?
			Signature: d.signDirectoryEntry(dirName),
			// WrappedKeys are by default the ones for the parent directory
			WrappedKeys: d.fetchKeys(parsed.Drop(1).Path()),
		},
	}
	body, err := json.Marshal(dirEntry)
	if err != nil {
		return zeroLoc, NewError(op, string(dirName), err)
	}
	data := bytes.NewBuffer(body)
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", d.serverURL), data)
	if err != nil {
		return zeroLoc, NewError(op, string(dirName), err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return zeroLoc, NewError(op, string(dirName), err)
	}

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return zeroLoc, NewError(op, string(dirName),
			errors.New(fmt.Sprintf("server error: %v", resp.StatusCode)))
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return zeroLoc, NewError(op, string(dirName), err)
	}
	loc, err := parser.LocationResponse(respBody)
	if err != nil {
		return zeroLoc, NewError(op, string(dirName), err)
	}
	if loc == nil {
		return zeroLoc, NewError(op, string(dirName), errors.New("server returned null Location"))
	}

	return *loc, nil
}

func (d *Directory) Glob(pattern string) ([]*upspin.DirEntry, error) {
	panic("Not implemented yet")
}

// fetchKeysExistingKeys fetches wrapped keys for the given directory (possibly cached).
func (d *Directory) fetchKeys(dirName upspin.PathName) []upspin.WrappedKey {
	// TODO: implement
	return []upspin.WrappedKey{upspin.WrappedKey{
		Hash:      [2]byte{1, 2},
		Encrypted: []byte{1, 2, 3},
	}}
}

// signDirectoryEntry uses this client's private key to sign the directory entry.
func (d *Directory) signDirectoryEntry(dirName upspin.PathName) []byte {
	return make([]byte, 32) // TODO: implement
}

func NewError(op string, path string, err error) *os.PathError {
	return &os.PathError{
		Op:   op,
		Path: path,
		Err:  err,
	}
}
