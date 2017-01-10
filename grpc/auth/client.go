// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	gContext "golang.org/x/net/context"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	pb "github.com/golang/protobuf/proto"
)

// Pinger describes part of a GRPC client for an upspin.Service
type Pinger interface {
	Ping(ctx gContext.Context, in *proto.PingRequest, opts ...grpc.CallOption) (*proto.PingResponse, error)
}

// Client is a partial upspin.Service that uses GRPC or HTTP as a transport
// and implements authentication using out-of-band headers.
// It should be embedded in any Upspin GRPC client implementation,
// or used as an HTTP handler. TODO(adg): remove the GRPC stuff
type Client interface {
	Ping() bool
	Close()

	// GRPC-specific methods. TODO(adg): remove these
	SetService(Pinger)
	GRPCConn() *grpc.ClientConn
	NewAuthContext() (ctx gContext.Context, opt grpc.CallOption, finishAuth func(error) error, err error)

	// Invoke calls the given RPC method ("Server.Method") with the
	// given request message and decodes the response in to the given
	// response message.
	Invoke(method string, req, resp pb.Message) error
}

// grpcClient is a partial upspin.Service that uses GRPC as transport and
// implements authentication using out-of-band GRPC headers.
// It should be embedded in any Upspin GRPC client implementation.
type grpcClient struct {
	pinger   Pinger
	grpcConn *grpc.ClientConn
	proxyFor upspin.Endpoint // the server is a proxy for this endpoint.

	keepAliveInterval time.Duration // interval of keep-alive packets.
	closeKeepAlive    chan bool     // channel used to tell the keep-alive routine to exit.

	clientAuth
}

// SecurityLevel defines the security required of a GRPC connection.
type SecurityLevel int

const (
	// Secure as the security argument to NewGRPCClient requires TLS
	// connections using CA certificates.
	Secure = SecurityLevel(iota + 1)

	// NoSecurity as the security argument to NewGRPCClient requires
	// connections with no authentication or encryption.
	NoSecurity

	// KeepAliveInterval is a suggested interval between keep-alive ping requests to the server.
	// A value of 0 means keep-alives are disabled. Google Cloud Platform (GCP) times out connections
	// every 10 minutes so a smaller values are recommended for talking to servers on GCP.
	KeepAliveInterval = 5 * time.Minute
)

// To be safe, we refresh the token 1 hour ahead of time.
var tokenFreshnessDuration = authTokenDuration - time.Hour

// NewClient returns new GRPC client connected to a GRPC server at a net address.
// The address is expected to be a raw network address with port number, as in domain.com:5580. However, for convenience,
// it is optionally accepted for the time being to use one of the following prefixes:
// https://, http://, grpc://. This may change in the future.
// A keep alive interval indicates the amount of time to send ping requests to the server. A duration of 0 disables
// keep-alive packets.
// The security level specifies the expected security guarantees of the connection.
// If proxyFor is an assigned endpoint, it indicates that this connection is being used to proxy request to that endpoint.
func NewClient(context upspin.Context, netAddr upspin.NetAddr, keepAliveInterval time.Duration, security SecurityLevel, proxyFor upspin.Endpoint) (Client, error) {
	const op = "grpc/auth.NewClient"
	if keepAliveInterval != 0 && keepAliveInterval < time.Minute {
		log.Info.Printf("Keep-alive interval too short. You may overload the server and be throttled")
	}
	addr := string(netAddr)
	isHTTP := strings.HasPrefix(addr, "http://")
	isHTTPS := strings.HasPrefix(addr, "https://")
	isGRPC := strings.HasPrefix(addr, "grpc://")
	skip := 0
	switch {
	case isHTTP, isGRPC:
		skip = 7
	case isHTTPS:
		skip = 8
	}
	ac := &grpcClient{
		keepAliveInterval: keepAliveInterval,
		closeKeepAlive:    make(chan bool, 1),
		proxyFor:          proxyFor,
	}
	ac.clientAuth.context = context
	opts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithDialer(ac.dialWithKeepAlive),
		grpc.WithTimeout(3 * time.Second),
	}
	var tlsConfig *tls.Config
	switch security {
	case NoSecurity:
		// No TLS config for no security.
	case Secure:
		tlsConfig = &tls.Config{RootCAs: context.CertPool()}
	default:
		return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid security level to NewGRPCClient: %v", security))
	}
	if tlsConfig != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		// Only allow insecure connections to the loop back network.
		if !isLocal(addr[skip:]) {
			return nil, errors.E(op, netAddr, errors.IO, errors.Str("insecure dial to non-loopback destination"))
		}
		opts = append(opts, grpc.WithInsecure())
	}
	var err error
	ac.grpcConn, err = grpc.Dial(addr[skip:], opts...)
	if err != nil {
		return nil, err
	}
	if keepAliveInterval != 0 {
		go ac.keepAlive()
	}
	return ac, nil
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

// keepAlive loops forever pinging the server every keepAliveInterval. It skips pings if there has been network
// activity more recently than the keep alive interval. It must run on a separate go routine.
func (ac *grpcClient) keepAlive() {
	sleepFor := ac.keepAliveInterval
	for {
		select {
		case <-time.After(sleepFor):
			lastIdleness := time.Since(ac.lastActivity())
			if lastIdleness < ac.keepAliveInterval {
				sleepFor = ac.keepAliveInterval - lastIdleness
				continue
			}
			sleepFor = ac.keepAliveInterval
			if !ac.Ping() {
				log.Error.Printf("grpc/auth: keepAlive: ping failed")
			}
			ac.setLastActivity()
		case <-ac.closeKeepAlive:
			return
		}
	}
}

// SetService sets the underlying RPC service which was obtained with
// proto.NewSERVICENAMEClient, where SERVICENAME is the RPC service definition
// from the proto file.
func (ac *grpcClient) SetService(p Pinger) {
	ac.pinger = p
}

// GRPCConn returns the GRPC client connection used to dial the server.
func (ac *grpcClient) GRPCConn() *grpc.ClientConn {
	return ac.grpcConn
}

func (ac *grpcClient) dialWithKeepAlive(target string, timeout time.Duration) (net.Conn, error) {
	// Invalidate auth token and mark proxy as not configured.
	ac.invalidateSession()

	c, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		if err := tc.SetKeepAlive(true); err != nil {
			return nil, err
		}
		if err := tc.SetKeepAlivePeriod(KeepAliveInterval); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Ping implements upspin.Service.
func (ac *grpcClient) Ping() bool {
	seq := rand.Int31()
	req := &proto.PingRequest{
		PingSequence: seq,
	}
	gctx, cancel := gContext.WithTimeout(gContext.Background(), 3*time.Second)
	defer cancel()
	resp, err := ac.pinger.Ping(gctx, req)
	if err != nil {
		log.Printf("Ping error: %s", err)
	}
	ac.setLastActivity()
	return err == nil && resp.PingSequence == seq
}

func (ac *grpcClient) isProxy() bool {
	return ac.proxyFor.Transport != upspin.Unassigned
}

// NewAuthContext sets up a gContext, GRPC CallOption, and finishAuth function
// for authenticating GRPC requests. If a request token is available, it puts
// that token in the context as GRPC metadata. If the request token is not
// available or has expired, it puts authentication request data in the
// context, and sets up a GRPC Call Option and finishAuth function for retrieving
// the authentication response from the GRPC response headers.
//
// Example usage:
// 	ctx, callOpt, finishAuth, err := ac.NewAuthContext()
// 	// handle err
// 	req := &proto.RequestMessage{ ... }
// 	resp, err := c.grpcClient.Echo(ctx, req, callOpt)
// 	err = finishAuth(err)
// 	// handle err
func (ac *grpcClient) NewAuthContext() (ctx gContext.Context, opt grpc.CallOption, finishAuth func(error) error, err error) {
	const op = "grpc/auth.NewAuthContext"

	ctx = gContext.Background()

	var header metadata.MD
	opt = grpc.Header(&header)

	if token, ok := ac.authToken(); ok {
		ctx = metadata.NewContext(ctx, metadata.Pairs(authTokenKey, token))
		finishAuth = func(err error) error {
			ac.setLastActivity()
			return err
		}
		return
	}

	// Authenticate client's user name. reqNow discourages signature replay.
	authMsg, err := signUser(ac.context, clientAuthMagic)
	if err != nil {
		log.Error.Printf("%s: signUser: %s", op, err)
		return nil, nil, nil, errors.E(op, err)
	}
	md := metadata.MD{authRequestKey: authMsg}
	if ac.isProxy() {
		md[proxyRequestKey] = []string{ac.proxyFor.String()}
	}
	ctx = metadata.NewContext(ctx, md)
	finishAuth = func(err error) error {
		ac.setLastActivity()
		if err != nil {
			return err
		}

		token, ok := header[authTokenKey]
		if !ok || len(token) != 1 {
			authErr, ok := header[authErrorKey]
			if !ok || len(authErr) != 1 {
				return errors.E(op, errors.Invalid, errors.Str("server did not respond to our authentication request"))
			}
			return errors.E(op, errors.Permission, errors.Str(authErr[0]))
		}

		// If talking to a proxy, make sure it is running as the same user.
		if ac.isProxy() {
			msg, ok := header[authRequestKey]
			if !ok {
				return errors.E(op, errors.Permission, errors.Str("proxy server must authenticate"))
			}
			if err := ac.verifyServerUser(msg); err != nil {
				log.Error.Printf("%s: client can't verify server user: %s", op, err)
				return errors.E(op, errors.Permission, err)
			}
		}

		ac.setAuthToken(token[0])
		return nil
	}
	return
}

// Close implements upspin.Service.
func (ac *grpcClient) Close() {
	select { // prevents blocking if Close is called more than once.
	case ac.closeKeepAlive <- true:
		close(ac.closeKeepAlive)
	default:
	}
	// The only error returned is ErrClientConnClosing, meaning something else has already caused it to close.
	_ = ac.grpcConn.Close() // explicitly ignore the error as there's nothing we can do.
}

func (ac *grpcClient) Invoke(method string, req, resp pb.Message) error {
	panic("grpcClient: Invoke not implemented")
}

type httpClient struct {
	client   *http.Client
	netAddr  upspin.NetAddr
	proxyFor upspin.Endpoint // the server is a proxy for this endpoint.

	clientAuth
}

// NewHTTPClient is like NewClient but it sets up an HTTP transport instead of GRPC.
// TODO(adg): replace NewClient with this function.
func NewHTTPClient(context upspin.Context, netAddr upspin.NetAddr, security SecurityLevel, proxyFor upspin.Endpoint) (Client, error) {
	const op = "grpc/auth.NewHTTPClient"

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
		return nil, errors.E(op, errors.Invalid, errors.Errorf("invalid security level to NewGRPCClient: %v", security))
	}

	// TODO(adg): Configure transport and client timeouts etc.
	t := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	if err := http2.ConfigureTransport(t); err != nil {
		return nil, errors.E(op, err)
	}
	c.client = &http.Client{Transport: t}

	return c, nil
}

func (c *httpClient) Invoke(method string, req, resp pb.Message) error {
	const op = "grpc/auth.Invoke"

	header := make(http.Header)

	token, haveToken := c.authToken()
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

func (c *httpClient) isProxy() bool {
	return c.proxyFor.Transport != upspin.Unassigned
}

// Stubs for unused methods.
func (c *httpClient) Ping() bool                 { return true }
func (c *httpClient) SetService(Pinger)          { panic("httpClient: SetService not implemented") }
func (c *httpClient) GRPCConn() *grpc.ClientConn { panic("httpClient: GRPCConn not implemented") }
func (c *httpClient) NewAuthContext() (ctx gContext.Context, opt grpc.CallOption, finishAuth func(error) error, err error) {
	panic("httpClient: NewAuthContext not implemented")
}
func (c *httpClient) Close() {}

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
