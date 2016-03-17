// Package auth handles authentication of Upspin users.
package auth

import (
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
	// Maximum allowed time between unauthenticated requests to the same server.
	AuthIntervalSec = 5 * 60 // 5 minutes
)

// NewClient returns a new HTTPClient that handles auth for the named user with the provided key pair and underlying HTTP client.
func NewClient(user upspin.UserName, keys upspin.KeyPair, httClient netutil.HTTPClientInterface) *HTTPClient {
	return &HTTPClient{
		user:   user,
		keys:   keys,
		client: httClient,
	}
}

// Do implements the interface
func (c *HTTPClient) Do(req *http.Request) (resp *http.Response, err error) {
	if req.URL == nil {
		// Let the native client handle this weirdness.
		return c.doWithoutAuth(req)
	}
	if req.URL.Scheme != "https" {
		// No point in doing authentication.
		return c.doWithoutAuth(req)
	}
	if c.url == nil || c.url.Host != req.URL.Host {
		// Must do auth again.
		return c.doAuth(req)
	}
	now := time.Now()
	if c.timeLastAuth.Add(time.Duration(AuthIntervalSec) * time.Second).Before(now) {
		return c.doAuth(req)
	}
	return c.doWithoutAuth(req)
}

// doWithoutAuth does not initially perform auth, but if the request fails with error code 401, we try exactly one more
// time with auth.
func (c *HTTPClient) doWithoutAuth(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode == 401 {
		return c.doAuth(req)
	}
	return resp, err
}

// doAuth performs authentication and caches the server and time of this last auth.
func (c *HTTPClient) doAuth(req *http.Request) (*http.Response, error) {
	err := signRequest(c.user, c.keys, req)
	if err != nil {
		return nil, err
	}
	c.url = req.URL
	c.timeLastAuth = time.Now()
	return c.client.Do(req)
}
