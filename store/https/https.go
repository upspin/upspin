// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package https implements the HTTPS transport protocol for upspin.Store.
package https

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"io"

	"upspin.io/bind"
	"upspin.io/upspin"
)

// Store is an implementation of upspin.Store that interfaces
// with an HTTP server for serving data.
type Store struct {
	upspin.NoConfiguration
	serverURL  string
	httpClient HTTPClient
}

// Guarantee we implement the interface
var _ upspin.Store = (*Store)(nil)

// maxBytesLimit is the maximum number of bytes to retrieve in one request.
const maxBytesLimit = 1 << 30 // 1GB

// Some error messages.
const (
	invalidRefError = "invalid reference"
	notHTTPError    = "not an HTTP(S) reference"
	httpClientError = "HTTP client error: %v"

	Get = "GET" // HTTP Get method
)

// HTTPClient is a minimal HTTP client interface. An instance of
// http.Client implements this interface.
type HTTPClient interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

// New returns a concrete implementation of Store, pointing to a
// server at a given URL (including the port), for performing Get and
// Put requests on blocks of data. Use this only for testing.
func New(serverURL string, httpClient HTTPClient) *Store {
	return &Store{
		serverURL:  serverURL,
		httpClient: httpClient,
	}
}

// IsServerReachable reports whether the server at an URL can be reached.
func IsServerReachable(serverURL string) bool {
	_, err := http.Head(serverURL)
	return err == nil
}

// Dial implements Dialer.
func (s *Store) Dial(context *upspin.Context, endpoint upspin.Endpoint) (upspin.Service, error) {
	const op = "Dial"
	if context == nil {
		return nil, newStoreError(op, "nil context", "")
	}
	serverURL, err := url.Parse(string(endpoint.NetAddr))
	if err != nil {
		return nil, newStoreError(op, fmt.Sprintf("invalid HTTP address for endpoint: %v", err), "")
	}
	s.serverURL = serverURL.String()
	if !IsServerReachable(s.serverURL) {
		return nil, newStoreError(op, "HTTPS store server unreachable", "")
	}
	return s, nil
}

// Ping implements Service.
func (s *Store) Ping() bool {
	return IsServerReachable(s.serverURL)
}

// ServerUserName implements Service.
func (s *Store) ServerUserName() string {
	return ""
}

// Get implements Store.
func (s *Store) Get(ref upspin.Reference) ([]byte, []upspin.Location, error) {
	const op = "Get"
	if ref == "" {
		return nil, nil, newStoreError(op, invalidRefError, "")
	}
	url := string(ref)
	if !strings.HasPrefix(string(ref), "http://") && !strings.HasPrefix(string(ref), "https://") {
		return nil, nil, newStoreError(op, notHTTPError, ref)
	}
	httpReq, err := http.NewRequest(Get, url, nil)
	if err != nil {
		return nil, nil, err
	}
	body, err := s.requestAndReadResponseBody(op, ref, httpReq)
	if err != nil {
		return nil, nil, err
	}
	return body, nil, nil
}

// Put implements Store.
func (s *Store) Put(data []byte) (upspin.Reference, error) {
	return "", errors.New("https: Put: not implemented")
}

// Delete implements Store.
func (s *Store) Delete(ref upspin.Reference) error {
	return errors.New("https: Delete: not implemented")
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
		return nil, newStoreError(op, fmt.Sprintf(httpClientError, resp.StatusCode), ref)
	}

	// Read the body of the response
	defer resp.Body.Close()
	respBody, err := BufferResponse(resp, maxBytesLimit)
	if err != nil {
		return nil, newStoreError(op, err.Error(), ref)
	}
	return respBody, nil
}

// Endpoint implements upspin.Service.
func (s *Store) Endpoint() upspin.Endpoint {
	return upspin.Endpoint{
		Transport: upspin.HTTPS,
		NetAddr:   upspin.NetAddr(s.serverURL),
	}
}

// Close implements upspin.Service.
func (s *Store) Close() {
	// Nothing to do.
}

// Authenticate implements upspin.Service.
func (s *Store) Authenticate(*upspin.Context) error {
	return nil
}

type storeError struct {
	op    string
	error string
	ref   upspin.Reference
}

// Error implements error
func (s storeError) Error() string {
	if s.ref != "" {
		return fmt.Sprintf("https: store error: %s: %s: %s", s.op, s.ref, s.error)
	}
	return fmt.Sprintf("https: store error: %s: %s", s.op, s.error)
}

func newStoreError(op string, error string, ref upspin.Reference) *storeError {
	return &storeError{
		op:    op,
		error: error,
		ref:   ref,
	}
}

// errTooLong is returned when a BufferResponse would not fit in the buffer budget.
var errTooLong = errors.New("response body too long")

// BufferResponse reads the body of an HTTP response up to maxBufLen bytes. It closes the response body.
// If the response is larger than maxBufLen, it returns ErrTooLong.
func BufferResponse(resp *http.Response, maxBufLen int64) ([]byte, error) {
	var buf []byte
	defer resp.Body.Close()
	if resp.ContentLength > 0 {
		if resp.ContentLength <= maxBufLen {
			buf = make([]byte, resp.ContentLength)
		} else {
			// Return an error
			return nil, errTooLong
		}
	} else {
		buf = make([]byte, maxBufLen)
	}
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		if err == io.ErrShortBuffer {
			return nil, errTooLong
		}
		return nil, err
	}
	return buf[:n], nil
}

func init() {
	// By default, set up only the HTTP client. The server URL gets bound at Dial time.
	bind.RegisterStore(upspin.HTTPS, New("", &http.Client{}))
}
