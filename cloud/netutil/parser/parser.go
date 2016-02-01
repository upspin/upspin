package parser

import (
	"encoding/json"
	"errors"
	"fmt"

	"upspin.googlesource.com/upspin.git/upspin"
)

// LocationResponse interprets the body of an HTTP response as
// Location and returns it. If it's not a Location, it tries to read
// an error message instead.
func LocationResponse(body []byte) (*upspin.Location, error) {
	var loc upspin.Location
	err := json.Unmarshal(body, &loc)
	if err != nil {
		return nil, ErrorResponse(body)
	}
	return &loc, nil
}

// DirEntryResponse interprets the body of an HTTP response as
// a DirEntry and returns it. If it's not a DirEntry, it tries to read
// an error message instead.
func DirEntryResponse(body []byte) (*upspin.DirEntry, error) {
	var dir upspin.DirEntry
	err := json.Unmarshal(body, &dir)
	if err != nil {
		return nil, ErrorResponse(body)
	}
	return &dir, nil
}

// ErrorResponse interprets the body of an HTTP response as a server
// error (which could contain the string "Success" for successful
// operations that do not return data).
func ErrorResponse(body []byte) error {
	serverErr := &struct {
		Error string
	}{}
	err := json.Unmarshal(body, serverErr)
	if err != nil {
		return errors.New(fmt.Sprintf("Can't parse reply from server: %v, %v", err, string(body)))
	}
	if serverErr.Error == "Success" {
		return nil
	}
	return errors.New(serverErr.Error)
}
