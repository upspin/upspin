// Package netutil implements http request/response, networking, and json-related utility functions
package netutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Sends an error in a json struct with an error message composed of
// a prefix and the actual error message.
func SendJSONError(w http.ResponseWriter, prefix string, error error) {
	SendJSONErrorString(w, fmt.Sprintf("%s%v", prefix, error.Error()))
}

// Sends a free-form error string in a json struct.
func SendJSONErrorString(w http.ResponseWriter, error string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(fmt.Sprintf("{error='%s'}", error)))
}

// SendJSONReply encodes a reply and sends it out on w as a JSON
// object. Make sure the reply type, if it's a struct (which it most
// likely will be) has *public* fields or nothing will be sent (just
// '{}').
func SendJSONReply(w http.ResponseWriter, reply interface{}) {
	js, err := json.Marshal(reply)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(js)
}

// BufferRequest allocates a buffer for reading this request. Use the
// smallest buffer possible, if size of request is known. Don't ever
// allocate more than maxBufLen. If buffer cannot be allocated, nil is
// returned and an error is sent to the client. Request is always
// closed after reading, even in case of error.
func BufferRequest(w http.ResponseWriter, r *http.Request, maxBufLen int64) []byte {
	var buf []byte
	defer r.Body.Close()
	if r.ContentLength > 0 {
		if r.ContentLength <= maxBufLen {
			buf = make([]byte, r.ContentLength)
		} else {
			// Return an error
			SendJSONErrorString(w, "Invalid request")
			return nil
		}
	} else {
		buf = make([]byte, maxBufLen)
	}
	n, err := r.Body.Read(buf)
	if err != io.EOF {
		SendJSONError(w, "reading request:", err)
		return nil
	}
	return buf[:n]
}
