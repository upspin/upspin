// Package gcpstore implements the interface upspin.Store for talking to Google Cloud Platform (GCP).
package gcpstore

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	serverError     = "server error: %v"
	invalidRefError = "invalid reference"
)

// Store is an implementation of upspin.Store that uses GCP to manage its storage.
type Store struct {
	serverURL  string
	httpClient netutil.HTTPClientInterface
}

// Guarantee we implement the interface
var _ upspin.Store = (*Store)(nil)

// New returns a concrete implementation of Store, pointing to a
// server at a given URL (including the port), for performing Get and
// Put requests on blocks of data. Use this only for testing.
func New(serverURL string, httpClient netutil.HTTPClientInterface) *Store {
	return &Store{
		serverURL:  serverURL,
		httpClient: httpClient,
	}
}

// Dial implements Dialer.
func (s *Store) Dial(context *upspin.Context, endpoint upspin.Endpoint) (interface{}, error) {
	const op = "Dial"
	if context == nil {
		return nil, newStoreError(op, "nil context", "")
	}
	serverURL, err := url.Parse(string(endpoint.NetAddr))
	if err != nil {
		return nil, newStoreError(op, fmt.Sprintf("invalid HTTP address for endpoint: %v", err), "")
	}
	s.serverURL = serverURL.String()
	authClient, isSecure := s.httpClient.(*auth.HTTPClient)
	if isSecure {
		authClient.SetUserName(context.UserName)
		authClient.SetUserKeys(auth.NewFactotum(context))
	}
	if !netutil.IsServerReachable(s.serverURL) {
		return nil, newStoreError(op, "Store server unreachable", "")
	}
	return s, nil
}

// ServerUserName implements Dialer.
func (s *Store) ServerUserName() string {
	return "GPC Store"
}

// Get implements Store.
func (s *Store) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	const op = "Get"
	if ref == "" {
		return nil, nil, newStoreError(op, invalidRefError, "")
	}
	var request string
	if strings.HasPrefix(string(ref), "http://") || strings.HasPrefix(string(ref), "https://") {
		request = string(ref)
	} else {
		request = fmt.Sprintf("%s/get?ref=%s", s.serverURL, ref)
	}
	httpReq, err := http.NewRequest(netutil.Get, request, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, newStoreError(op, fmt.Sprintf(serverError, err), ref)
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
		loc, err := parser.LocationResponse(body)
		if err != nil {
			return nil, nil, newStoreError(op, err.Error(), ref)
		}
		// If the server did not specify the endpoint, it's
		// implicitly there; patch it.
		if len(loc.Endpoint.NetAddr) == 0 {
			loc.Endpoint.NetAddr = upspin.NetAddr(s.serverURL)
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
		return body, nil, nil
	}
	// NOT REACHED
}

// Put implements Store.
func (s *Store) Put(data []byte) (upspin.Reference, error) {
	const op = "Put"
	var zeroRef upspin.Reference
	bufFrom := bytes.NewBuffer(data)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "dummy")
	if err != nil {
		return zeroRef, newStoreError(op, err.Error(), "")
	}
	_, err = io.Copy(fw, bufFrom)
	if err != nil {
		return zeroRef, err
	}
	err = w.Close()
	if err != nil {
		return zeroRef, err
	}
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", s.serverURL), &body)
	if err != nil {
		return zeroRef, err
	}
	req.Header.Set(netutil.ContentType, w.FormDataContentType())

	// Submit the request
	respBody, err := s.requestAndReadResponseBody(op, "", req)
	if err != nil {
		return zeroRef, err
	}

	// Parse the response
	ref, err := parser.ReferenceResponse(respBody)
	if err != nil {
		return zeroRef, newStoreError(op, fmt.Sprintf(serverError, err), "")
	}
	if ref == "" {
		return zeroRef, newStoreError(op, invalidRefError, "")
	}
	return ref, nil
}

// Delete implements Store.
func (s *Store) Delete(ref upspin.Reference) error {
	const op = "Delete"
	if ref == "" {
		return newStoreError(op, invalidRefError, "")
	}
	// TODO: check if we own the file or otherwise are allowed to delete it.
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/delete?ref=%s", s.serverURL, ref), nil)
	if err != nil {
		return err
	}

	respBody, err := s.requestAndReadResponseBody(op, ref, req)
	if err != nil {
		return err
	}

	// Parse the response for any errors
	err = parser.ErrorResponse(respBody)
	if err != nil {
		return err
	}
	return nil
}

// requestAndReadResponseBody is an internal helper function that
// sends a given request over the HTTP client and parses the body of
// the reply, using op and key to build an error if one is
// encountered along the way.
func (s *Store) requestAndReadResponseBody(op string, ref upspin.Reference, req *http.Request) ([]byte, error) {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, newStoreError(op, err.Error(), ref)
	}

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return nil, newStoreError(op, fmt.Sprintf(serverError, resp.StatusCode), ref)
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, newStoreError(op, err.Error(), ref)
	}
	return respBody, nil
}

// Endpoint implements Store.
func (s *Store) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(s.serverURL),
	}
}

type storeError struct {
	op    string
	error string
	ref   upspin.Reference
}

// Error implements error
func (s storeError) Error() string {
	if s.ref != "" {
		return fmt.Sprintf("Store: %s: %s: %s", s.op, s.ref, s.error)
	}
	return fmt.Sprintf("Store: %s: %s", s.op, s.error)
}

func newStoreError(op string, error string, ref upspin.Reference) *storeError {
	return &storeError{
		op:    op,
		error: error,
		ref:   ref,
	}
}

func init() {
	// By default, set up only the HTTP client. The server URL gets bound at Dial time.
	bind.RegisterStore(upspin.GCP, New("", auth.NewAnonymousClient(&http.Client{})))
}
