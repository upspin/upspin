package parser

import (
	"encoding/json"
	"errors"
	"log"

	"upspin.googlesource.com/upspin.git/upspin"
)

// LocationResponse interprets the body of an HTTP response as
// Location and returns it. If it's not a Location, it tries to read
// an error message instead.
func LocationResponse(body []byte) (*upspin.Location, error) {
	var loc upspin.Location
	err := json.Unmarshal(body, &loc)
	if err != nil {
		log.Printf("Error in unmarshal: %v", err)
		srverr := &struct{ error string }{}
		err = json.Unmarshal(body, &srverr)
		if err != nil {
			return nil, errors.New("Can't parse reply from server")
		}
		return nil, errors.New(srverr.error)
	}
	return &loc, nil
}
