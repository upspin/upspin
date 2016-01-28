// Package store implements the interface upspin.Store for talking to Google Cloud Platform (GCP).
package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
)

// Store is an implementation of upspin.Store that uses GCP to manage its storage.
type Store struct {
	serverURL string
	client    HTTPClientInterface
}

// Guarantee we implement the interface
var _ upspin.Store = (*Store)(nil)

// HTTPClientInterface is a minimal HTTP client interface. An instance of
// http.Client satisfies the interface.
type HTTPClientInterface interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

// New returns a concrete implementation of Store, pointing to a
// server at a given URL (including the port), for performing Get and
// Put requests on blocks of data.
func New(serverURL string, client HTTPClientInterface) *Store {
	return &Store{
		serverURL: serverURL,
		client:    client,
	}
}

func (s *Store) Get(location upspin.Location) ([]byte, []upspin.Location, error) {
	if location.Reference.Key == "" {
		return nil, nil, NewStoreError("Key can't be empty", "")
	}
	key := location.Reference.Key
	var request string
	switch location.Reference.Packing {
	case upspin.HTTP:
		request = location.Reference.Key
	case upspin.EllipticalEric:
		request = fmt.Sprintf("%s/get?ref=%s", s.serverURL, key)
	default:
		return nil, nil, NewStoreError("Can't figure out the packing", key)
	}
	httpReq, err := http.NewRequest(netutil.Get, request, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, nil, NewStoreError(fmt.Sprintf("Error getting data from server: %v", err), key)
	}
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we
	// read and return?
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	answerType := resp.Header.Get(netutil.ContentType)
	switch answerType {
	case "application/json":
		// This is either a re-location reply or an error.
		loc, err := s.parseLocationResponse(key, body)
		if err != nil {
			return nil, nil, err
		}
		locs := []upspin.Location{*loc}
		return nil, locs, nil
	case "text/plain", "text/plain; charset=utf-8", "application/x-gzip":
		// We got our data inline in 'body'. Just return it.
		return body, nil, nil
	default:
		// We go on a limb here and assume it was some other
		// valid type that we don't know about such as an
		// unencrypted image or a pdf file.
		log.Printf("%s: %v", netutil.ContentType, answerType)
		return body, nil, nil
	}
	// NOT REACHED
}

// parseLocationResponse interprets the body of an HTTP response as
// Location and returns it. If it's not a Location, it tries to read
// an error message from the server. A key is given for purposes of
// setting the "path" of the error, if necessary.
func (s *Store) parseLocationResponse(key string, body []byte) (*upspin.Location, error) {
	var loc upspin.Location
	err := json.Unmarshal(body, &loc)
	if err != nil {
		fmt.Printf("Error in unmarshal: %v", err)
		srverr := &struct{ error string }{}
		err = json.Unmarshal(body, &srverr)
		if err != nil {
			return nil, NewStoreError("Can't parse reply from server", key)
		}
		return nil, NewStoreError(srverr.error, key)
	}
	return &loc, nil
}

func (s *Store) Put(ref upspin.Reference, data []byte) (upspin.Location, error) {
	var zeroLoc upspin.Location // The zero value.
	bufFrom := bytes.NewBuffer(data)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", ref.Key)
	if err != nil {
		return zeroLoc, NewStoreError("Can't create multi-part form to upload", ref.Key)
	}
	_, err = io.Copy(fw, bufFrom)
	if err != nil {
		return zeroLoc, err
	}
	err = w.Close()
	if err != nil {
		return zeroLoc, err
	}
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", s.serverURL), &body)
	if err != nil {
		return zeroLoc, err
	}
	req.Header.Set(netutil.ContentType, w.FormDataContentType())

	// Submit the request
	resp, err := s.client.Do(req)
	if err != nil {
		return zeroLoc, NewStoreError(fmt.Sprintf("Error putting data to server: %v", err), ref.Key)
	}

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return zeroLoc, NewStoreError(fmt.Sprintf("error uploading to server: %v", resp.StatusCode), ref.Key)
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return zeroLoc, err
	}

	// Parse the response
	newLoc, err := s.parseLocationResponse(ref.Key, respBody)
	if newLoc == nil {
		return zeroLoc, err
	}
	return *newLoc, err
}

func (s *Store) NetAddr() upspin.NetAddr {
	panic("not defined yet")
}

// Implements Error
type StoreError struct {
	error string
	key   string
}

func (s StoreError) Error() string {
	return s.error
}

func (s StoreError) Key() string {
	return s.key
}

func NewStoreError(error string, key string) *StoreError {
	return &StoreError{
		error: error,
		key:   key,
	}
}
