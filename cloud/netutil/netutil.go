// Package netutil implements http request/response, networking, and JSON-related utility functions
package netutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	// Constants that may appear in HTTP headers:

	// ContentType is the content type.
	ContentType = "Content-Type"

	// ContentLength is the content length.
	ContentLength = "Content-Length"

	// HTTP Methods:

	// Get is the GET method.
	Get = "GET"

	// Post is the POST method.
	Post = "POST"

	// Patch is the PATCH method.
	Patch = "PATCH"

	// Put is the PUT method.
	Put = "PUT"
)

var (
	ErrTooLong = errors.New("response body too long")
)

// TODO(edpin): Rename this to get rid of 'Interface'.

// HTTPClientInterface is a minimal HTTP client interface. An instance of
// http.Client implements this interface.
type HTTPClientInterface interface {
	Do(req *http.Request) (resp *http.Response, err error)
}

// SendJSONError sends an error in a JSON struct with an error message
// composed of a prefix and the actual error message.
func SendJSONError(resp http.ResponseWriter, prefix string, error error) {
	SendJSONErrorString(resp, fmt.Sprintf("%s%v", prefix, error.Error()))
}

// SendJSONErrorString sends a free-form error string in a JSON struct.
func SendJSONErrorString(resp http.ResponseWriter, error string) {
	resp.Header().Set(ContentType, "application/json")
	resp.Write([]byte(fmt.Sprintf(`{"error":%q}`, error)))
}

// SendJSONReply encodes a reply and sends it out on w as a JSON
// object. Make sure the reply type, if it's a struct (which it most
// likely will be) has *public* fields or nothing will be sent (just
// '{}').
func SendJSONReply(resp http.ResponseWriter, reply interface{}) {
	js, err := json.Marshal(reply)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Header().Set(ContentType, "application/json")
	resp.Write(js)
}

// BufferRequest reads the body of the request 'req' into a buffer of
// size up to maxBufLen bytes. If a buffer cannot be allocated to fit
// the request, nil is returned and an error is sent back to the
// client via 'resp'. The request body is always closed after reading,
// even in case of an error.
func BufferRequest(resp http.ResponseWriter, req *http.Request, maxBufLen int64) []byte {
	var buf []byte
	defer req.Body.Close()
	if req.ContentLength > 0 {
		if req.ContentLength <= maxBufLen {
			buf = make([]byte, req.ContentLength)
		} else {
			// Return an error
			SendJSONErrorString(resp, "invalid request")
			return nil
		}
	} else {
		buf = make([]byte, maxBufLen)
	}
	n, err := req.Body.Read(buf)
	if err != nil && err != io.EOF {
		SendJSONError(resp, "read:", err)
		return nil
	}
	return buf[:n]
}

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
			return nil, ErrTooLong
		}
	} else {
		buf = make([]byte, maxBufLen)
	}
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		if err == io.ErrShortBuffer {
			return nil, ErrTooLong
		}
		return nil, err
	}
	return buf[:n], nil
}

// IsServerReachable reports whether the server at an URL can be reached.
func IsServerReachable(serverURL string) bool {
	_, err := http.Get(serverURL)
	return err == nil
}
