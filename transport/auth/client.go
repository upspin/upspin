// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"time"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"

	pb "github.com/golang/protobuf/proto"
)

// Client is a partial upspin.Service that uses HTTP as a transport
// and implements authentication using out-of-band headers.
type Client interface {
	Ping() bool
	Close()

	// Invoke calls the given RPC method ("Server.Method") with the
	// given request message and decodes the response in to the given
	// response message.
	Invoke(method string, req, resp pb.Message) error
}

// SecurityLevel defines the security required of a connection.
type SecurityLevel int

const (
	// Secure as the security argument to NewClient requires TLS
	// connections using CA certificates.
	Secure = SecurityLevel(iota + 1)

	// NoSecurity as the security argument to NewClient requires
	// connections with no authentication or encryption.
	NoSecurity
)

// To be safe, we refresh the token 1 hour ahead of time.
var tokenFreshnessDuration = authTokenDuration - time.Hour

type httpClient struct {
	client   *http.Client
	netAddr  upspin.NetAddr
	proxyFor upspin.Endpoint // the server is a proxy for this endpoint.

	clientAuth
}

// NewClient returns a new client that speaks to an HTTP server at a net
// address. The address is expected to be a raw network address with port
// number, as in domain.com:5580. The security level specifies the expected
// security guarantees of the connection. If proxyFor is an assigned endpoint,
// it indicates that this connection is being used to proxy request to that
// endpoint.
func NewClient(context upspin.Context, netAddr upspin.NetAddr, security SecurityLevel, proxyFor upspin.Endpoint) (Client, error) {
	const op = "transport/auth.NewClient"

	c := &httpClient{
		netAddr:  netAddr,
		proxyFor: proxyFor,
	}
	c.clientAuth.context = context

	var tlsConfig *tls.Config
	switch security {
	case NoSecurity:
		// Only allow insecure connections to the loop back network.
		if !isLocal(string(netAddr)) {
			return nil, errors.E(op, netAddr, errors.IO, errors.Str("insecure dial to non-loopback destination"))
		}
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
	case Secure:
		tlsConfig = &tls.Config{RootCAs: context.CertPool()}
	default:
		return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid security level to NewClient: %v", security))
	}

	// TODO(adg): Configure transport and client timeouts etc.
	t := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	// TOOD(adg): Re-enable HTTP/2 once it's fast enough to be usable.
	//if err := http2.ConfigureTransport(t); err != nil {
	//	return nil, errors.E(op, err)
	//}
	c.client = &http.Client{Transport: t}

	return c, nil
}

func (c *httpClient) Invoke(method string, req, resp pb.Message) error {
	const op = "transport/auth.Invoke"

	header := make(http.Header)

	token, haveToken := c.authToken()
retryAuth:
	if haveToken {
		// If we have a token already, supply it.
		header.Set(authTokenHeader, token)
	} else {
		// Otherwise prepare an auth request.
		// Authenticate client's user name. reqNow discourages signature replay.
		authMsg, err := signUser(c.context, clientAuthMagic)
		if err != nil {
			log.Error.Printf("%s: signUser: %s", op, err)
			return errors.E(op, err)
		}
		header[authRequestHeader] = authMsg
		if c.isProxy() {
			header.Set(proxyRequestHeader, c.proxyFor.String())
		}
	}

	// Encode the payload.
	payload, err := pb.Marshal(req)
	if err != nil {
		return errors.E(op, err)
	}
	header.Set("Content-Type", "application/octet-stream")

	// Make the HTTP request.
	url := fmt.Sprintf("https://%s/api/%s", c.netAddr, method)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return errors.E(op, errors.Invalid, err)
	}
	httpReq.Header = header
	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	body, err := ioutil.ReadAll(httpResp.Body)
	httpResp.Body.Close()
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	if httpResp.StatusCode != http.StatusOK {
		if haveToken && bytes.Contains(body, []byte(errUnauthenticated.Error())) {
			// If the server restarted it will have forgotten about
			// our session, and so our auth token becomes invalid.
			// Invalidate the session and retry this request,
			c.invalidateSession()
			haveToken = false // Retry exactly once.
			goto retryAuth
			// TODO(adg): refactor to get rid of the goto
		}
		return errors.E(op, errors.IO, errors.Errorf("%s: %s", httpResp.Status, body))
	}

	// Decode the response.
	if err := pb.Unmarshal(body, resp); err != nil {
		return errors.E(op, errors.Invalid, err)
	}
	c.setLastActivity()

	if haveToken {
		// If we already had a token, we're done.
		return nil
	}
	// Otherwise, process the authentication response.

	// Store the returned authentication token.
	token = httpResp.Header.Get(authTokenHeader)
	if len(token) == 0 {
		authErr := httpResp.Header.Get(authErrorHeader)
		if len(authErr) == 0 {
			return errors.E(op, errors.Invalid, errors.Str("server did not respond to our authentication request"))
		}
		return errors.E(op, errors.Permission, errors.Str(authErr))
	}

	// If talking to a proxy, make sure it is running as the same user.
	if c.isProxy() {
		msg, ok := httpResp.Header[authRequestHeader]
		if !ok {
			return errors.E(op, errors.Permission, errors.Str("proxy server must authenticate"))
		}
		if err := c.verifyServerUser(msg); err != nil {
			log.Error.Printf("%s: client can't verify server user: %s", op, err)
			return errors.E(op, errors.Permission, err)
		}
	}

	c.setAuthToken(token)
	return nil
}

func isLocal(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func (c *httpClient) isProxy() bool {
	return c.proxyFor.Transport != upspin.Unassigned
}

// Stubs for unused methods.
func (c *httpClient) Ping() bool { return true }
func (c *httpClient) Close()     {}

// clientAuth tracks the auth token and its freshness.
type clientAuth struct {
	context upspin.Context

	mu              sync.Mutex // protects the fields below.
	token           string
	lastRefresh     time.Time
	lastNetActivity time.Time // last known time of some network activity.
}

// lastActivity reports the time of the last known network activity.
func (ca *clientAuth) lastActivity() time.Time {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ca.lastNetActivity
}

// setLastActivity records the current time as that of the last known network activity.
// It is used to prevent unnecessarily frequent pings.
func (ca *clientAuth) setLastActivity() {
	ca.mu.Lock()
	ca.lastNetActivity = time.Now()
	ca.mu.Unlock()
}

// invalidateSession forgets the authentication token.
func (ca *clientAuth) invalidateSession() {
	ca.mu.Lock()
	ca.token = ""
	ca.mu.Unlock()
}

// authToken returns the current authentication token and true,
// or - if no valid token is held - an empty string and false.
func (ca *clientAuth) authToken() (token string, ok bool) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.token == "" || ca.lastRefresh.Add(tokenFreshnessDuration).Before(time.Now()) {
		return "", false
	}
	return ca.token, true
}

// setAuthToken sets the authentication token to the given value.
func (ca *clientAuth) setAuthToken(token string) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.token = token
	ca.lastRefresh = time.Now()
}

// verifyServerUser ensures server is running as the same user.
// It assumes that msg[0] is the user name.
func (ca *clientAuth) verifyServerUser(msg []string) error {
	u := upspin.UserName(msg[0])
	if ca.context.UserName() != u {
		return errors.Errorf("client %s does not match server %s", ca.context.UserName(), u)
	}

	// Get user's public key.
	keyServer, err := bind.KeyServer(ca.context, ca.context.KeyEndpoint())
	if err != nil {
		return err
	}
	key, err := keyServer.Lookup(u)
	if err != nil {
		return err
	}

	// Validate signature.
	err = verifyUser(key.PublicKey, msg, serverAuthMagic, time.Now())
	if err != nil {
		return err
	}

	return nil
}
