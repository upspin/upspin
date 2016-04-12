package main

// This file handles contacting the Store server. We don't use gcpstore because we don't want cross-dependencies between
// servers and clients. Also, we don't have a context. We do, however, have a private key for identity purposes.
// TODO(ehg): figure out where to derive the directory server's private key from.

import (
	"fmt"
	"net/http"

	"strings"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/parser"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	maxBufLen = 1024 * 1024 // 1MB is the max size we fetch from the Store.
)

var (
	dirServerName = upspin.UserName("upspin-dir@upspin.io")
	// TODO(ehg): reach for the heart attack medication now.
	dirServerKeys = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n12753464240987498461983148972112771989345466331613802629415048914390179140074\n113057636188867924404487991022758917365734968737512850580456009882603240653118"),
		Private: upspin.PrivateKey("68857421579026555549183754996095119843468813533440727584492238901752627939653"),
	}
)

// storeClient is an authenticating HTTP client that talks to an Upspin Store server.
type storeClient struct {
	http *auth.HTTPClient
}

func newStoreClient(http *auth.HTTPClient) *storeClient {
	return &storeClient{
		http: http,
	}
}

// innerGet returns the contents of a reference pointed to by a location or returns new locations where to look.
// It implicitly binds to the endpoint in the location.
func (s *storeClient) innerGet(loc *upspin.Location) ([]byte, []upspin.Location, error) {
	const op = "GetRef"
	ref := loc.Reference
	var url string
	if strings.HasPrefix(string(ref), "http://") || strings.HasPrefix(string(ref), "https://") {
		url = string(ref)
	} else {
		url = fmt.Sprintf("%s/get?ref=%s", loc.Endpoint.NetAddr, loc.Reference)
	}

	logMsg.Printf("Making Get request to Store: %s", url)
	req, err := http.NewRequest(netutil.Get, url, nil)
	if err != nil {
		return nil, nil, err
	}
	buf, answerType, err := s.requestAndReadResponseBody(op, req)
	if err != nil {
		return nil, nil, err
	}
	switch answerType {
	case "application/json":
		// This is either a re-location reply or an error.
		loc, err := parser.LocationResponse(buf)
		if err != nil {
			return nil, nil, err
		}
		// If the server did not specify the endpoint, it's
		// implicitly there; patch it.
		if len(loc.Endpoint.NetAddr) == 0 {
			loc.Endpoint.NetAddr = upspin.NetAddr(loc.Endpoint.NetAddr)
		}
		locs := []upspin.Location{*loc}
		return nil, locs, nil
	}
	return buf, nil, nil
}

// Get fetches the contents of a reference pointed to by a location. It implicitly binds to the endpoint in the location.
func (s *storeClient) Get(loc *upspin.Location) ([]byte, error) {
	// TODO: this only does one indirection. Fix it.
	buf, locs, err := s.innerGet(loc)
	if err != nil {
		return nil, err
	}
	if len(locs) > 0 {
		buf, _, err := s.innerGet(&locs[0])
		return buf, err
	}
	return buf, err
}

// requestAndReadResponseBody sends a request over the HTTP client and reads the body of
// the reply up to a safe limit. It returns the bytes read and the answer type (a string such as "text/html").
func (s *storeClient) requestAndReadResponseBody(op string, req *http.Request) ([]byte, string, error) {
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", newDirError(op, "", fmt.Sprintf("store server error code: %d", resp.StatusCode))
	}
	// Read the body of the response
	buf, err := netutil.BufferResponse(resp, maxBufLen)
	if err != nil {
		return nil, "", newDirError(op, "", err.Error())
	}
	return buf, resp.Header.Get(netutil.ContentType), nil
}
