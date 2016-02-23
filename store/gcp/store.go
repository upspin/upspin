// Package store implements the interface upspin.Store for talking to Google Cloud Platform (GCP).
package store

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	serverError     = "%v: server error: %v"
	invalidKeyError = "invalid key"
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

func (s *Store) Dial(context *upspin.Context, endpoint upspin.Endpoint) (interface{}, error) {
	if context == nil {
		return nil, NewStoreError("nil context", "")
	}
	serverURL, err := url.Parse(string(endpoint.NetAddr))
	if err != nil {
		return nil, NewStoreError(fmt.Sprintf("invalid HTTP address for endpoint: %v", err), "")
	}
	s.serverURL = serverURL.String()
	return s, nil
}

func (s *Store) ServerUserName() string {
	return "GPC Store"
}

func (s *Store) Get(key string) ([]byte, []upspin.Location, error) {
	if key == "" {
		return nil, nil, NewStoreError(invalidKeyError, "")
	}
	var request string
	if strings.HasPrefix(key, "http://") || strings.HasPrefix(key, "https://") {
		request = key
	} else {
		request = fmt.Sprintf("%s/get?ref=%s", s.serverURL, key)
	}
	httpReq, err := http.NewRequest(netutil.Get, request, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, NewStoreError(fmt.Sprintf(serverError, "Get", err), key)
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
			return nil, nil, NewStoreError(err.Error(), key)
		}
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

func (s *Store) Put(data []byte) (string, error) {
	const op = "Put"
	var zeroKey string
	bufFrom := bytes.NewBuffer(data)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "dummy")
	if err != nil {
		return zeroKey, NewStoreError(fmt.Sprintf("%v: multi-part form error: %v", op, err), "")
	}
	_, err = io.Copy(fw, bufFrom)
	if err != nil {
		return zeroKey, err
	}
	err = w.Close()
	if err != nil {
		return zeroKey, err
	}
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/put", s.serverURL), &body)
	if err != nil {
		return zeroKey, err
	}
	req.Header.Set(netutil.ContentType, w.FormDataContentType())

	// Submit the request
	respBody, err := s.requestAndReadResponseBody(op, "", req)
	if err != nil {
		return zeroKey, err
	}

	// Parse the response
	key, err := parser.KeyResponse(respBody)
	if err != nil {
		return zeroKey, NewStoreError(fmt.Sprintf(serverError, op, err), "")
	}
	if key == "" {
		return zeroKey, NewStoreError(invalidKeyError, "")
	}
	return key, nil
}

func (s *Store) Delete(key string) error {
	if key == "" {
		return NewStoreError(invalidKeyError, "")
	}
	// TODO: check if we own the file or otherwise are allowed to delete it.
	req, err := http.NewRequest(netutil.Post, fmt.Sprintf("%s/delete?ref=%s", s.serverURL, key), nil)
	if err != nil {
		return err
	}

	respBody, err := s.requestAndReadResponseBody("Delete", key, req)
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
func (s *Store) requestAndReadResponseBody(op string, key string, req *http.Request) ([]byte, error) {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, NewStoreError(fmt.Sprintf("%v: %v", op, err), key)
	}

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return nil, NewStoreError(fmt.Sprintf(serverError, op, resp.StatusCode), key)
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, NewStoreError(fmt.Sprintf("%v: %v", op, err), key)
	}
	return respBody, nil
}

func (s *Store) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(s.serverURL),
	}
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

func init() {
	// By default, set up only the HTTP client. The server URL gets bound at Dial time.
	access.RegisterStore(upspin.GCP, New("", &http.Client{}))
}
