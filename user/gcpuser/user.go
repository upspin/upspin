// Package gcpuser implements the interface upspin.User for talking to a server on the Google Cloud Platform (GCP).
package gcpuser

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/cache"
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

// lookupEntry is the cached result of a Lookup call.
type lookupEntry struct {
	endpoints   []upspin.Endpoint
	keys        []upspin.PublicKey
	timeFetched time.Time
}

const (
	serverError         = "server error code %d"
	lookupCacheDuration = time.Hour * 2
)

var lookupCache = cache.NewLRU(100) // <upspin.UserName, lookupEntry>

func (u *user) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	if entry, found := lookupCache.Get(name); found {
		e := entry.(lookupEntry)
		if !e.timeFetched.Add(lookupCacheDuration).Before(time.Now()) {
			// Cache is still valid. We're done.
			return e.endpoints, e.keys, nil
		}
		// expired
	}
	endpoints, keys, err := u.doLookup(name)
	if err != nil {
		return nil, nil, err
	}
	le := lookupEntry{
		endpoints:   endpoints,
		keys:        keys,
		timeFetched: time.Now(),
	}
	lookupCache.Add(name, le)
	return endpoints, keys, nil
}

func (u *user) doLookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
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
	if context == nil {
		return nil, newUserError(fmt.Errorf("nil context"), "")
	}
	serverURL, err := url.Parse(string(endpoint.NetAddr))
	if err != nil {
		return nil, err
	}
	if !netutil.IsServerReachable(serverURL.String()) {
		return nil, newUserError(fmt.Errorf("User server unreachable"), "")
	}
	instance := &user{
		serverURL:  serverURL.String(),
		httpClient: &http.Client{},
		endpoint:   endpoint,
	}
	return instance, nil
}

func (u *user) ServerUserName() string {
	return "GCP User"
}

func (u *user) Endpoint() upspin.Endpoint {
	return u.endpoint
}

// Ping implements upspin.Service.
func (u *user) Ping() bool {
	return netutil.IsServerReachable(u.serverURL)
}

// Close implements upspin.Service.
func (u *user) Close() {
	// TODO
}

// Authenticate implements upspin.Service.
func (u *user) Authenticate(*upspin.Context) error {
	// TODO
	return nil
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
