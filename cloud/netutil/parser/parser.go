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
		return nil, parseError(body)
	}
	return &loc, nil
}

func DirEntryResponse(body []byte) (*upspin.DirEntry, error) {
	var dir upspin.DirEntry
	err := json.Unmarshal(body, &dir)
	if err != nil {
		return nil, parseError(body)
	}
	return &dir, nil
}

func parseError(body []byte) error {
	srverr := &struct{ Error string }{}
	err := json.Unmarshal(body, &srverr)
	if err != nil {
		return errors.New(fmt.Sprintf("Can't parse reply from server: %v", err))
	}
	return errors.New(srverr.Error)
}
