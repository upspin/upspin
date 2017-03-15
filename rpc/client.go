// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc // import "upspin.io/rpc"

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
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

	// Invoke calls the given RPC method ("Server/Method") with the
	// given request message and decodes the response into the given
	// response message.
	// For regular one-shot methods, the stream and done channels must be nil.
	// For streaming RPC methods, the caller should provide a nil response
	// and non-nil stream and done channels.
	Invoke(method string, req, resp pb.Message, stream ResponseChan, done <-chan struct{}) error
}

// ResponseChan describes a mechanism to report streamed messages to a client
// (the caller of Client.Invoke). Typically this interface should wrap a
// channel that carries decoded protocol buffers.
type ResponseChan interface {
	// Send sends a proto-encoded message to the client.
	// If done is closed, the send should abort.
	Send(b []byte, done <-chan struct{}) error

	// Error sends an error condition to the client.
	Error(error)

	// Close closes the response channel.
	Close()
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
	baseURL  string
	proxyFor upspin.Endpoint // the server is a proxy for this endpoint.

	clientAuth
}

// NewClient returns a new client that speaks to an HTTP server at a net
// address. The address is expected to be a raw network address with port
// number, as in domain.com:5580. The security level specifies the expected
// security guarantees of the connection. If proxyFor is an assigned endpoint,
// it indicates that this connection is being used to proxy request to that
// endpoint.
func NewClient(cfg upspin.Config, netAddr upspin.NetAddr, security SecurityLevel, proxyFor upspin.Endpoint) (Client, error) {
	const op = "rpc.NewClient"

	c := &httpClient{
		proxyFor: proxyFor,
	}
	c.clientAuth.config = cfg

	var tlsConfig *tls.Config
	switch security {
	case NoSecurity:
		// Only allow insecure connections to the loop back network.
		if !isLocal(string(netAddr)) {
			return nil, errors.E(op, errors.IO, errors.Errorf("insecure dial to non-loopback destination %q", netAddr))
		}
		c.baseURL = "http://" + string(netAddr)
	case Secure:
		tlsConfig = &tls.Config{RootCAs: cfg.CertPool()}
		c.baseURL = "https://" + string(netAddr)
	default:
		return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid security level to NewClient: %v", security))
	}

	t := &http.Transport{
		TLSClientConfig: tlsConfig,
		// The following values are the same as
		// net/http.DefaultTransport.
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	// TOOD(adg): Re-enable HTTP/2 once it's fast enough to be usable.
	//if err := http2.ConfigureTransport(t); err != nil {
	//	return nil, errors.E(op, err)
	//}
	c.client = &http.Client{Transport: t}

	return c, nil
}

func (c *httpClient) Invoke(method string, req, resp pb.Message, stream ResponseChan, done <-chan struct{}) error {
	const op = "rpc.Invoke"

	if (resp == nil) == (stream == nil) {
		return errors.E(op, errors.Str("exactly one of resp and stream must be nil"))
	}

	token, haveToken := c.authToken()

retryAuth:
	header := make(http.Header)
	if haveToken {
		// If we have a token already, supply it.
		header.Set(authTokenHeader, token)
	} else {
		// Otherwise prepare an auth request.
		authMsg, err := signUser(c.config, clientAuthMagic, serverAddr(c))
		if err != nil {
			log.Error.Printf("%s: signUser: %s", op, err)
			return errors.E(op, err)
		}
		header.Set(authRequestHeader, strings.Join(authMsg, ","))
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
	url := fmt.Sprintf("%s/api/%s", c.baseURL, method)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return errors.E(op, errors.Invalid, err)
	}
	httpReq.Header = header
	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return errors.E(op, errors.IO, err)
	}
	c.setLastActivity()
	body := httpResp.Body

	if httpResp.StatusCode != http.StatusOK {
		msg, _ := ioutil.ReadAll(body)
		body.Close()
		if haveToken && bytes.Contains(msg, []byte(errUnauthenticated.Error())) {
			// If the server restarted it will have forgotten about
			// our session, and so our auth token becomes invalid.
			// Invalidate the session and retry this request,
			c.invalidateSession()
			haveToken = false // Retry exactly once.
			goto retryAuth
		}
		return errors.E(op, errors.IO, errors.Errorf("%s: %s", httpResp.Status, msg))
	}

	if resp != nil {
		// One-shot method, decode the response.
		respBytes, _ := ioutil.ReadAll(body)
		body.Close()
		if err != nil {
			return errors.E(op, errors.IO, err)
		}
		if err := pb.Unmarshal(respBytes, resp); err != nil {
			return errors.E(op, errors.Invalid, err)
		}
	}

	if haveToken {
		// If we already had a token, we're done.
		if stream != nil {
			go decodeStream(stream, body, done)
		}
		return nil
	}
	// Otherwise, process the authentication response.

	// Store the returned authentication token.
	token = httpResp.Header.Get(authTokenHeader)
	if len(token) == 0 {
		authErr := httpResp.Header.Get(authErrorHeader)
		if len(authErr) > 0 {
			body.Close()
			return errors.E(op, errors.Permission, errors.Str(authErr))
		}
		// No authentication token returned, but no error either.
		// The server doesn't care about authenticating this request.
		// Proceed.
	}

	// If talking to a proxy, make sure it is running as the same user.
	if c.isProxy() {
		msg, ok := httpResp.Header[authRequestHeader]
		if !ok {
			body.Close()
			return errors.E(op, errors.Permission, errors.Str("proxy server must authenticate"))
		}
		if err := c.verifyServerUser(msg); err != nil {
			body.Close()
			return errors.E(op, errors.Permission, err)
		}
	}

	if len(token) > 0 {
		c.setAuthToken(token)
	}

	if stream != nil {
		go decodeStream(stream, body, done)
	}
	return nil
}

// decodeStream reads a stream of protobuf-encoded messages from r and sends
// them (without decoding them) to the given stream. If the done channel is
// closed then the stream and reader are closed and decodeStream returns.
func decodeStream(stream ResponseChan, r io.ReadCloser, done <-chan struct{}) {
	defer stream.Close()
	defer r.Close()

	// A stream begins with the bytes "OK".
	var ok [2]byte
	if _, err := readFull(r, ok[:], done); err == io.ErrUnexpectedEOF {
		// Server closed the stream.
		return
	} else if err != nil {
		stream.Error(errors.E(errors.IO, err))
		return
	}
	if ok[0] != 'O' || ok[1] != 'K' {
		stream.Error(errors.E(errors.IO, errors.Str("unexpected stream preamble")))
	}

	var msgLen [4]byte
	var buf []byte
	for {
		// Messages are of the form
		// [length, 4 byte, big-endian-encoded int32]
		// [length bytes of encoded protobuf message]
		if _, err := readFull(r, msgLen[:], done); err == io.ErrUnexpectedEOF {
			return
		} else if err != nil {
			stream.Error(errors.E(errors.IO, err))
			return
		}

		l := binary.BigEndian.Uint32(msgLen[:])

		if cap(buf) < int(l) {
			buf = make([]byte, l)
		} else {
			buf = buf[:l]
		}
		if _, err := readFull(r, buf, done); err != nil {
			stream.Error(errors.E(errors.IO, err))
			return
		}

		if err := stream.Send(buf, done); err != nil {
			stream.Error(errors.E(errors.IO, err))
			return
		}
	}
}

// readFull is like io.ReadFull but it will return io.EOF if the provided
// channel is closed.
func readFull(r io.Reader, b []byte, done <-chan struct{}) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		// TODO(adg): this may leak goroutines if the requisite reads
		// never complete, but will that actually happen? It would be
		// great to have something like this hooked into the runtime.
		n, err := io.ReadFull(r, b)
		ch <- result{n, err}
	}()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-done:
		return 0, io.EOF
	}
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
	config upspin.Config

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

func serverAddr(c *httpClient) string {
	if c.isProxy() {
		return string(c.proxyFor.NetAddr)
	}
	if strings.HasPrefix(c.baseURL, "https://") {
		return c.baseURL[8:]
	}
	if strings.HasPrefix(c.baseURL, "http://") {
		return c.baseURL[7:]
	}
	panic("no recognizable server") // can't happen
}

// verifyServerUser ensures server is running as the same user.
// It assumes that msg[0] is the user name.
func (ca *clientAuth) verifyServerUser(msg []string) error {
	u := upspin.UserName(msg[0])
	if ca.config.UserName() != u {
		return errors.Errorf("client %s does not match server %s", ca.config.UserName(), u)
	}

	// Get user's public key.
	keyServer, err := bind.KeyServer(ca.config, ca.config.KeyEndpoint())
	if err != nil {
		return err
	}
	key, err := keyServer.Lookup(u)
	if err != nil {
		return err
	}

	// Validate signature.
	err = verifyUser(key.PublicKey, msg, serverAuthMagic, "[localproxy]", time.Now())
	if err != nil {
		return err
	}

	return nil
}
