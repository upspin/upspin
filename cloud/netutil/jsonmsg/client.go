// Package jsonmsg handles marshaling and unmarshaling Upspin data structures
// to and from JSON. The goal of this package is to hide types that only exist
// on the wire and to strongly type JSON server replies to the extent possible.
package jsonmsg

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
	zeroWhichAccess        whichAccessMessage
)

// The messages in this package only exist on the wire and as such are not exported, to avoid confusion with their
// very similar counterparts defined in upspin.go and other places.

type refMessage struct {
	Ref upspin.Reference
}

type errorMessage struct {
	Error string
}

// whichAccessMessage is a message used to reply to Directory.WhichAccess.
type whichAccessMessage struct {
	Access upspin.PathName // The path to an Access file, if not empty.
}

// userEntry stores publicly known information for a given user.
type userEntry struct {
	User      upspin.UserName    // User's email address (e.g. bob@bar.com).
	Keys      []upspin.PublicKey // Known keys for the user.
	Endpoints []upspin.Endpoint  // Known endpoints for the user's directory entry.
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
	// json.Unmarshal does not return an error if a message does not fit the format expected. Instead, it leaves it
	// empty. But empty Access is a valid server response. So we use some non-empty string to differentiate
	// an error from an unparsed message.
	const nonEmpty = "bla"
	access := whichAccessMessage{
		Access: nonEmpty,
	}
	err := json.Unmarshal(body, &access)
	if err != nil || access.Access == nonEmpty {
		return "", ErrorResponse(body)
	}
	return access.Access, nil
}

// UserLookupResponse interprets the body of an HTTP response as the user name, endpoints
// and public keys. If the body doesn't conform, it tries to read an error message instead.
func UserLookupResponse(body []byte) (upspin.UserName, []upspin.Endpoint, []upspin.PublicKey, error) {
	if len(body) == 0 {
		return "", nil, nil, errEmptyServerResponse
	}
	var ue userEntry
	err := json.Unmarshal(body, &ue)
	if err != nil || len(ue.User) == 0 {
		return "", nil, nil, ErrorResponse(body)
	}
	return ue.User, ue.Endpoints, ue.Keys, nil
}
