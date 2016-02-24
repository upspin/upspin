// The user service implements two main pieces of functionality for the Upspin service: root directory
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
)

// userServer is the implementation of the User Service on GCP.
type userServer struct {
	cloudClient gcp.Interface
	httpClient  netutil.HTTPClientInterface
}

// userEntry stores all known information for a given user. The fields
// are exported because JSON parsing needs access to them.
type userEntry struct {
	User      string            // User's email address (e.g. bob@bar.com).
	Keys      [][]byte          // Known keys for the user.
	Endpoints []upspin.Endpoint // Known endpoints for the user's directory entry.
}

const (
	minKeyLen = 12
)

var (
	projectId       = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName      = flag.String("bucket", "g-upspin-user", "The name of an existing bucket within the project.")
	errKeyTooShort  = errors.New("key length too short")
	errInvalidEmail = errors.New("invalid email format")
)

// validateUserEmail checks whether the given email is valid. For
// now, it only checks the form "a@b.co", but in the future it could
// verify DNS entries and perform other checks. A nil error indicates
// validity.
func validateUserEmail(userEmail string) error {
	if len(userEmail) < 6 {
		return errInvalidEmail
	}
	if strings.Index(userEmail, "@") < 1 {
		return errInvalidEmail
	}
	if strings.Index(userEmail, ".") < 3 {
		return errInvalidEmail
	}
	return nil
}

// validateKey checks whether a key appears to conform to one of the
// known key formats. It does not reject unknown formats, but it does
// reject keys that are too short to be valid in any current of future
// format. A nil error indicates validity.
func validateKey(key []byte) error {
	if len(key) < minKeyLen {
		return errKeyTooShort
	}
	return nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// addKeyHandler handles the HTTP PUT/POST request for adding a new
// key for a given user. Required parameters are user=<email> and
// key=<bytes>. Minimal validation is done on the email, just to check
// for proper form. Very minimal validation is done on the key.
func (u *userServer) addKeyHandler(w http.ResponseWriter, r *http.Request) {
	context := "addkey: "
	user := u.preambleParseRequestAndGetUser(context, netutil.Post, w, r)
	if user == "" {
		// An error has already been sent out on w.
		return
	}

	key := []byte(r.FormValue("key"))
	err := validateKey(key)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Appends to the current user entry, if any.
	ue, err := u.fetchUserEntry(user)
	if err != nil {
		// If this is a Not Found error, then allocate a new userEntry and continue.
		if isNotFound(err) {
			log.Printf("User %q not found on GCP, adding new one", user)
			ue = &userEntry{
				User: user,
				Keys: make([][]byte, 0, 1),
			}
		} else {
			netutil.SendJSONError(w, context, err)
			return
		}
	}
	// Append the key and save
	ue.Keys = append(ue.Keys, []byte(key))
	err = u.putUserEntry(user, ue)
	if err != nil {
		netutil.SendJSONError(w, context, err)
	}
	netutil.SendJSONErrorString(w, "success")
}

// addRootHandler handles the HTTP PUT/POST request for adding a new
// directory endpoint for a given user. Required parameters are user=<email> and
// endpoint=<upspin.Endpoint>. Minimal validation is done on the email, just to check
// for proper form. Very minimal validation is done on the endpoint.
func (u *userServer) addRootHandler(w http.ResponseWriter, r *http.Request) {
	context := "addroot: "
	user := u.preambleParseRequestAndGetUser(context, netutil.Post, w, r)
	if user == "" {
		// An error has already been sent out on w.
		return
	}

	// Parse the new endpoint
	endpointJSON := []byte(r.FormValue("endpoint"))
	if len(endpointJSON) == 0 {
		netutil.SendJSONErrorString(w, "empty endpoint")
		return
	}
	var endpoint upspin.Endpoint
	err := json.Unmarshal(endpointJSON, &endpoint)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(user)
	if err != nil {
		// If this is a Not Found error, then allocate a new userEntry and continue.
		if isNotFound(err) {
			log.Printf("User %q not found on GCP, adding new one", user)
			ue = &userEntry{
				User:      user,
				Endpoints: make([]upspin.Endpoint, 0, 1),
			}
		} else {
			netutil.SendJSONError(w, context, err)
			return
		}
	}
	// Append the endpoint and save
	ue.Endpoints = append(ue.Endpoints, endpoint)
	err = u.putUserEntry(user, ue)
	if err != nil {
		netutil.SendJSONError(w, context, err)
	}
	netutil.SendJSONErrorString(w, "success")
}

// getHandler handles the /get request to lookup the user
// information. The user=<email> parameter is required.
func (u *userServer) getHandler(w http.ResponseWriter, r *http.Request) {
	context := "get: "
	user := u.preambleParseRequestAndGetUser(context, netutil.Get, w, r)
	if user == "" {
		// An error has already been sent out on w.
		return
	}
	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(user)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Reply to user
	netutil.SendJSONReply(w, *ue)
}

func (u *userServer) deleteHandler(w http.ResponseWriter, r *http.Request) {
	netutil.SendJSONErrorString(w, "not implemented")
}

// preambleParseRequestAndGetUser performs the common tasks between
// all the Handler functions and returns the user present in the
// request, or the empty string if one is not found. An error message
// is sent on the response if an error is encountered.
func (u *userServer) preambleParseRequestAndGetUser(context string, expectedReqType string, w http.ResponseWriter, r *http.Request) string {
	// Validate request type
	switch expectedReqType {
	case netutil.Post:
		if r.Method != "POST" && r.Method != "PUT" {
			netutil.SendJSONErrorString(w, fmt.Sprintf("%sonly handles POST http requests", context))
			return ""
		}
	case netutil.Get:
		if r.Method != "GET" {
			netutil.SendJSONErrorString(w, fmt.Sprintf("%sonly handles GET http requests", context))
			return ""
		}
	default:
	}
	// Parse the form and validate the user parameter
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return ""
	}
	user := r.FormValue("user")
	if err = validateUserEmail(user); err != nil {
		netutil.SendJSONError(w, context, err)
		return ""
	}
	return user
}

// fetchUserEntry reads the user entry for a given user from permanent storage on GCP.
func (u *userServer) fetchUserEntry(user string) (*userEntry, error) {
	// Get the link to where the user entry is stored
	link, err := u.cloudClient.Get(user)
	if err != nil {
		return nil, err
	}
	// Now go fetch that link.
	req, err := http.NewRequest(netutil.Get, link, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading GCP link response %v", err)
	}
	// Now convert it to a userEntry
	var ue userEntry
	err = json.Unmarshal(buf, &ue)
	if err != nil {
		return nil, err
	}
	log.Printf("Fetched user entry for %s", user)
	return &ue, nil
}

// putUserEntry writes the user entry for a user to permanent storage on GCP.
func (u *userServer) putUserEntry(user string, userEntry *userEntry) error {
	if userEntry == nil {
		return errors.New("nil userEntry")
	}
	jsonBuf, err := json.Marshal(userEntry)
	if err != nil {
		return fmt.Errorf("conversion to json failed: %v", err)
	}
	_, err = u.cloudClient.Put(user, jsonBuf)
	return err
}

// new creates a UserService from a pre-configured GCP instance and an HTTP client.
func new(cloudClient gcp.Interface, httpClient netutil.HTTPClientInterface) *userServer {
	u := &userServer{
		cloudClient: cloudClient,
		httpClient:  httpClient,
	}
	return u
}

func main() {
	u := new(gcp.New(*projectId, *bucketName, gcp.DefaultWriteACL), &http.Client{})
	http.HandleFunc("/addkey", u.addKeyHandler)
	http.HandleFunc("/addroot", u.addRootHandler)
	http.HandleFunc("/delete", u.deleteHandler)
	http.HandleFunc("/get", u.getHandler)
	log.Println("Starting user service...")
	log.Fatal(http.ListenAndServe(":8082", nil))
}
