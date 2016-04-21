package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	errEmptyServerResponse = errors.New("empty server response")
	zeroLoc                upspin.Location
	zeroRef                refMessage
	zeroErr                errorMessage
	zeroWhichAccess        WhichAccessMessage
)

// TODO: export all of these so we don't duplicate them or write wrapper functions to send them out on the wire.
type refMessage struct {
	Ref upspin.Reference
}

type errorMessage struct {
	Error string
}

// WhichAccessMessage is a message used to reply to Directory.WhichAccess.
type WhichAccessMessage struct {
	Access upspin.PathName
}

// LocationResponse interprets the body of an HTTP response as
// Location and returns it. If it's not a Location, it tries to read
// an error message instead.
func LocationResponse(body []byte) (*upspin.Location, error) {
	if len(body) == 0 {
		return nil, errEmptyServerResponse
	}
	var loc upspin.Location
	err := json.Unmarshal(body, &loc)
	if err != nil || loc == zeroLoc {
		return nil, ErrorResponse(body)
	}
	return &loc, nil
}

// ReferenceResponse interprets the body of an HTTP response as a reference in a
// proper JSON structure (i.e. "{ref:'foo'}"). If it's not in the
// format of a reference, it tries to read an error message instead.
func ReferenceResponse(body []byte) (upspin.Reference, error) {
	if len(body) == 0 {
		return "", errEmptyServerResponse
	}
	var ref refMessage
	err := json.Unmarshal(body, &ref)
	if err != nil || ref == zeroRef {
		return "", ErrorResponse(body)
	}
	return ref.Ref, nil
}

// DirEntryResponse interprets the body of an HTTP response as
// a DirEntry and returns it. If it's not a DirEntry, it tries to read
// an error message instead.
func DirEntryResponse(body []byte) (*upspin.DirEntry, error) {
	if len(body) == 0 {
		return nil, errEmptyServerResponse
	}
	var dir upspin.DirEntry
	err := json.Unmarshal(body, &dir)
	if err != nil || len(dir.Name) == 0 {
		return nil, ErrorResponse(body)
	}
	return &dir, nil
}

// ErrorResponse interprets the body of an HTTP response as a server
// error (which could contain the string "Success" for successful
// operations that do not return data).
func ErrorResponse(body []byte) error {
	if len(body) == 0 {
		return errEmptyServerResponse
	}
	var serverErr errorMessage
	err := json.Unmarshal(body, &serverErr)
	if err != nil || serverErr == zeroErr {
		// This is likely a serious problem because the server
		// returned one of the other structures or an
		// ErrorMessage. If none of them parse, there's likely
		// a client/server version mismatch somewhere.
		strErr := fmt.Sprintf("can't parse reply from server: %v, %v", err, string(body))
		log.Println(strErr)
		return errors.New(strErr)
	}
	if serverErr.Error == "success" {
		return nil
	}
	return errors.New(serverErr.Error)
}

// WhichAccessResponse interprets the body of an HTTP response as the
// path name to an Access file. If the body is not a path name, it tries to
// read an error message instead.
func WhichAccessResponse(body []byte) (upspin.PathName, error) {
	if len(body) == 0 {
		return "", errEmptyServerResponse
	}
	var access WhichAccessMessage
	err := json.Unmarshal(body, &access)
	if err != nil || access == zeroWhichAccess {
		return "", ErrorResponse(body)
	}
	return access.Access, nil
}
