// Package gcpuser implements the interface upspin.User for talking to a server on the Google Cloud Platform (GCP).
package gcpuser

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
)

type user struct {
	serverURL  string
	httpClient netutil.HTTPClientInterface
}

var _ upspin.User = (*user)(nil)

// userEntry stores all known information for a given user. The fields
// are exported because JSON parsing needs access to them.
type userEntry struct {
	User      string            // User's email address (e.g. bob@bar.com).
	Keys      [][]byte          // Known keys for the user.
	Endpoints []upspin.Endpoint // Known endpoints for the user's directory entry.
}

const (
	serverError = "server error code %d"
)

func (u *user) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	req, err := http.NewRequest(netutil.Get, fmt.Sprintf("%s/get?user=%s", u.serverURL, name), nil)
	if err != nil {
		return nil, nil, newUserError(err, name)
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, nil, newUserError(err, name)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, newUserError(fmt.Errorf(serverError, resp.StatusCode), name)
	}
	// Check the content type
	answerType := resp.Header.Get(netutil.ContentType)
	if !strings.HasPrefix(answerType, "application/json") {
		return nil, nil, newUserError(fmt.Errorf("invalid response format: %v", answerType), name)
	}

	// Read the body of the response
	defer resp.Body.Close()
	// TODO(edpin): maybe add a limit here to the size of bytes we return?
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, newUserError(err, name)
	}

	var ue userEntry
	err = json.Unmarshal(respBody, &ue)
	if err != nil {
		return nil, nil, newUserError(err, name)
	}
	// Last check so we know the server is not crazy
	if ue.User != string(name) {
		return nil, nil, newUserError(fmt.Errorf("invalid user returned %s", ue.User), name)
	}
	// Convert keys returned by server to appropriate type
	keys := make([]upspin.PublicKey, 0, len(ue.Keys))
	for _, k := range ue.Keys {
		keys = append(keys, upspin.PublicKey(k))
	}
	return ue.Endpoints, keys, nil
}

func (u *user) Dial(context *upspin.Context, endpoint upspin.Endpoint) (interface{}, error) {
	if context == nil {
		return nil, newUserError(fmt.Errorf("nil context"), "")
	}
	serverURL, err := url.Parse(string(endpoint.NetAddr))
	if err != nil {
		return nil, err
	}
	u.serverURL = serverURL.String()
	return u, nil
}

func (u *user) ServerUserName() string {
	return "GCP User"
}

// Implements Error
type userError struct {
	error error
	user  upspin.UserName
}

func (e userError) Error() string {
	return fmt.Sprintf("user: %s: %s", e.user, e.error.Error())
}

func newUserError(error error, user upspin.UserName) *userError {
	return &userError{
		error: error,
		user:  user,
	}
}

func init() {
	access.RegisterUser(upspin.GCP, &user{
		serverURL:  "",
		httpClient: &http.Client{},
	})
}
