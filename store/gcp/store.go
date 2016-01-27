// Package store implements the interface upspin.Store.
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

// The concrete implementation of upspin.Store
type StoreClient struct {
	addr   upspin.NetAddr
	client HttpClientInterface
}

// HttpClientInterface is a minimal http client interface. An instance of
// http.Client suffices.
type HttpClientInterface interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

// Creates a concrete implementation of Store, pointing to a server at a given url and port.
func New(serverUrl string, port int16, client HttpClientInterface) *StoreClient {
	addr := upspin.NetAddr{
		Server: serverUrl,
		Port:   port,
	}
	return &StoreClient{
		addr:   addr,
		client: client,
	}
}

func (s *StoreClient) Get(location upspin.Location) ([]byte, []upspin.Location, error) {
	if location.Reference.Key == "" {
		return nil, nil, StoreError{"Key can't be empty"}
	}
	var request string
	switch location.Reference.Protocol {
	case upspin.HTTP:
		request = location.Reference.Key
	case upspin.EllipticalEric:
		request = fmt.Sprintf("%s/get?ref=%s", s.addr.String(), location.Reference.Key)
	default:
		return nil, nil, StoreError{"Can't figure out the protocol"}
	}
	httpReq, err := http.NewRequest(netutil.Get, request, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, nil, StoreError{fmt.Sprintf("Error getting data from server: %v", err)}
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
		loc, err := s.parseLocationResponse(body)
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

// parseLocationResponse interprets the body of an http response as
// Location and returns it. If it's not a Location, it tries to read
// an error message from the server.
func (s *StoreClient) parseLocationResponse(body []byte) (*upspin.Location, error) {
	var loc upspin.Location
	err := json.Unmarshal(body, &loc)
	if err != nil {
		fmt.Printf("Error in unmarshal: %v", err)
		srverr := &struct{ error string }{}
		err = json.Unmarshal(body, &srverr)
		if err != nil {
			return nil, StoreError{"Can't parse reply from server"}
		}
		return nil, StoreError{srverr.error}
	}
	return &loc, nil
}

func (s *StoreClient) Put(ref upspin.Reference, data []byte) (upspin.Location, error) {
	var loc upspin.Location
	bufFrom := bytes.NewBuffer(data)
	body := bytes.Buffer{}
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", ref.Key)
	if err != nil {
		return loc, StoreError{"Can't create multi-part form to upload"}
	}
	io.Copy(fw, bufFrom)
	err = w.Close()
	if err != nil {
		return loc, err
	}
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", s.addr.String()), &body)
	if err != nil {
		return loc, err
	}
	req.Header.Set(netutil.ContentType, w.FormDataContentType())

	// Submit the request
	resp, err := s.client.Do(req)
	if err != nil {
		return loc, StoreError{fmt.Sprintf("Error putting data to server: %v", err)}
	}

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return loc, StoreError{fmt.Sprintf("error uploading to server: %v", resp.StatusCode)}
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return loc, err
	}

	// Parse the response
	newLoc, err := s.parseLocationResponse(respBody)
	return *newLoc, err
}

func (s *StoreClient) NetAddr() upspin.NetAddr {
	return s.addr
}

// Implements Error
type StoreError struct {
	error string
}

func (s StoreError) Error() string {
	return s.error
}
