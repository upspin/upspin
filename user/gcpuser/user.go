// Package gcpuser implements the interface upspin.User for talking to a server on the Google Cloud Platform (GCP).
package gcpuser

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/jsonmsg"
	"upspin.googlesource.com/upspin.git/upspin"
)

type user struct {
	upspin.NoConfiguration
	endpoint   upspin.Endpoint
	serverURL  string
	httpClient netutil.HTTPClientInterface
}

var _ upspin.User = (*user)(nil)

const (
	serverError = "server error code %d"
)

func (u *user) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	// TODO(edpin): we should cache user roots and keys for a few hours as these things change infrequently.
	// It is akin to DNS changes, so a few hours is okay (or a hard reset of the client).
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

	user, endpoints, keys, err := jsonmsg.UserLookupResponse(respBody)
	if err != nil {
		return nil, nil, err
	}
	// Last check so we know the server is not crazy
	if user != name {
		return nil, nil, newUserError(fmt.Errorf("invalid user returned %s", user), name)
	}
	return endpoints, keys, nil
}

func (u *user) Dial(context *upspin.Context, endpoint upspin.Endpoint) (upspin.Service, error) {
	// TODO(edpin): this works because we mostly only use one instance and talk to the same URL. However, it will
	// break if we create two instances, each pointing to a different key server. Fix it.
	if context == nil {
		return nil, newUserError(fmt.Errorf("nil context"), "")
	}
	serverURL, err := url.Parse(string(endpoint.NetAddr))
	if err != nil {
		return nil, err
	}
	u.serverURL = serverURL.String()
	if !netutil.IsServerReachable(u.serverURL) {
		return nil, newUserError(fmt.Errorf("User server unreachable"), "")
	}
	u.endpoint = endpoint
	return u, nil
}

func (u *user) ServerUserName() string {
	return "GCP User"
}

func (u *user) Endpoint() upspin.Endpoint {
	return u.endpoint
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
	bind.RegisterUser(upspin.GCP, &user{
		serverURL:  "",
		httpClient: &http.Client{},
	})
}
