// Package auth handles authentication of Upspin users.
package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
)

// HTTPClient is a thin wrapper around a standard HTTP Client that implements authentication transparently. It caches state
// so that not every request needs to be signed. HTTPClient is optimized to work with a single server endpoint.
// It will work with any number of servers, but it keeps state about the last one, so using it with many servers will
// decrease its performance.
type HTTPClient struct {
	// Caches the base URL of the last server connected with.
	url *url.URL

	// Records the time we last authenticated with the server.
	// NOTE: this may seem like a premature optimization, but a comment in tls.ConnectionState indicates that
	// resumed connections don't get a TLS unique token, which prevents us from implicitly authenticating the
	// connection. To prevent a round-trip to the server, we preemptly re-auth every AuthIntervalSec
	timeLastAuth time.Time

	// The user we authenticate for.
	user upspin.UserName

	// The user's keys.
	keys upspin.KeyPair

	// The underlying HTTP client
	client netutil.HTTPClientInterface
}

var _ netutil.HTTPClientInterface = (*HTTPClient)(nil)

const (
	// AuthIntervalSec is the maximum allowed time between unauthenticated requests to the same server.
	AuthIntervalSec = 5 * 60 // 5 minutes
)

var (
	errNoUser = &clientError{"no user set"}
	errNoKeys = &clientError{"no keys set"}
)

// NewClient returns a new HTTPClient that handles auth for the named user with the provided key pair and underlying HTTP client.
func NewClient(user upspin.UserName, keys upspin.KeyPair, httClient netutil.HTTPClientInterface) *HTTPClient {
	return &HTTPClient{
		user:   user,
		keys:   keys,
		client: httClient,
	}
}

// NewAnonymousClient returns a new HTTPClient that does not yet know about the user name or user keys.
// To complete setup, use SetUserName and SetUserKeys.
func NewAnonymousClient(httClient netutil.HTTPClientInterface) *HTTPClient {
	return &HTTPClient{
		client: httClient,
	}
}

// SetUserName sets the user name for this HTTPClient instance.
func (c *HTTPClient) SetUserName(user upspin.UserName) {
	c.user = user
}

// SetUserKeys sets the user keys for this HTTPClient instance.
func (c *HTTPClient) SetUserKeys(keys upspin.KeyPair) {
	c.keys = keys
}

// Do implements netutil.HTTPClientInterface.
func (c *HTTPClient) Do(req *http.Request) (resp *http.Response, err error) {
	c.prepareRequest(req)
	if req.URL == nil {
		// Let the native client handle this weirdness.
		return c.doWithoutSign(req)
	}
	if req.URL.Scheme != "https" {
		// No point in doing authentication.
		return c.doWithoutSign(req)
	}
	if c.url == nil || c.url.Host != req.URL.Host {
		// Must sign gain.
		return c.doWithSign(req)
	}
	// It's better to avoid a round trip and sign requests that can't be played back.
	if !isRequestReplayable(req) {
		return c.doWithSign(req)
	}
	now := time.Now()
	if c.timeLastAuth.Add(time.Duration(AuthIntervalSec) * time.Second).Before(now) {
		return c.doWithSign(req)
	}
	return c.doWithoutSign(req)
}

// prepareRequest sets the necessary fields for auth on the request, common to both signed and unsigned requests.
func (c *HTTPClient) prepareRequest(req *http.Request) {
	req.Header.Set(userNameHeader, string(c.user)) // Set the username
	req.Header.Set("Date", time.Now().Format(time.RFC850))
}

// isRequestReplayable reports whether the request can be played back to the server safely.
func isRequestReplayable(req *http.Request) bool {
	if req.Body == nil && req.Method != "POST" {
		// A request without payload is always replayable
		return true
	}
	// Note: In general, if the body can seek to the beginning again, it should be replayable. However, Go's HTTP
	// client peeks inside the buffer, making it hard for us to wrap another buffer with an implentation of
	// io.ReadSeeker (it can be resolved, but not without reverse-engineering the native HTTP client, which is a bad idea).
	return false
}

// doWithoutSign does not initially sign the request, but if the request fails with error code 401, we try up to one more
// time with signing, if possible.
func (c *HTTPClient) doWithoutSign(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return resp, newError(err)
	}
	if resp.StatusCode == http.StatusUnauthorized && req.URL.Scheme == "https" {
		if isRequestReplayable(req) {
			return c.doWithSign(req)
		}
	}
	return resp, err
}

// doAuth performs signature authentication and caches the server and time of this last signed request.
func (c *HTTPClient) doWithSign(req *http.Request) (*http.Response, error) {
	if c.user == "" {
		return nil, errNoUser
	}
	var zeroKeys upspin.KeyPair
	if c.keys == zeroKeys {
		return nil, errNoKeys
	}
	err := signRequest(c.user, c.keys, req)
	if err != nil {
		return nil, newError(err)
	}
	c.url = req.URL
	c.timeLastAuth = time.Now()
	return c.client.Do(req)
}

type clientError struct {
	errorMsg string
}

// Error implements error
func (c *clientError) Error() string {
	return fmt.Sprintf("HTTPClient: %s", c.errorMsg)
}

func newError(err error) error {
	return &clientError{err.Error()}
}
