// Package netutil implements http request/response, networking, and json-related utility functions
package netutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	// Constants that may appear in HTTP headers
	ContentType = "Content-Type"

	// HTTP Methods
	Get  = "GET"
	Post = "POST"
)

// SendJSONError sends an error in a json struct with an error message
// composed of a prefix and the actual error message.
func SendJSONError(resp http.ResponseWriter, prefix string, error error) {
	SendJSONErrorString(resp, fmt.Sprintf("%s%v", prefix, error.Error()))
}

// SendJSONErrorString sends a free-form error string in a json struct.
func SendJSONErrorString(resp http.ResponseWriter, error string) {
	resp.Header().Set(ContentType, "application/json")
	resp.Write([]byte(fmt.Sprintf("{error:%q}", error)))
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
			SendJSONErrorString(resp, "Invalid request")
			return nil
		}
	} else {
		buf = make([]byte, maxBufLen)
	}
	n, err := req.Body.Read(buf)
	if err != nil && err != io.EOF {
		SendJSONError(resp, "reading request:", err)
		return nil
	}
	return buf[:n]
}
