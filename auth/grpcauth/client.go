// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package grpcauth

import (
	"crypto/tls"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"upspin.io/bind"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// GRPCCommon is an interface that all GRPC services implement for authentication and ping as part of upspin.Service.
type GRPCCommon interface {
	// Ping is the GRPC call for Ping.
	Ping(ctx gContext.Context, in *proto.PingRequest, opts ...grpc.CallOption) (*proto.PingResponse, error)
}

// AuthClientService is a partial Service that uses GRPC as transport and implements Authentication.
type AuthClientService struct {
	grpcCommon GRPCCommon
	grpcConn   *grpc.ClientConn
	context    upspin.Context
	proxyFor   upspin.Endpoint // the server is a proxy for this endpoint.

	keepAliveInterval time.Duration // interval of keep-alive packets.
	closeKeepAlive    chan bool     // channel used to tell the keep-alive routine to exit.

	mu               sync.Mutex // protects the field below.
	authToken        string
	lastTokenRefresh time.Time
	lastNetActivity  time.Time // last known time of some network activity.
	proxyConfigured  bool
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

// NewGRPCClient returns new GRPC client connected to a GRPC server at a net address.
// The address is expected to be a raw network address with port number, as in domain.com:5580. However, for convenience,
// it is optionally accepted for the time being to use one of the following prefixes:
// https://, http://, grpc://. This may change in the future.
// A keep alive interval indicates the amount of time to send ping requests to the server. A duration of 0 disables
// keep-alive packets.
// The security level specifies the expected security guarantees of the connection.
// If proxyFor is an assigned endpoint, it indicates that this connection is being used to proxy request to that endpoint.
func NewGRPCClient(context upspin.Context, netAddr upspin.NetAddr, keepAliveInterval time.Duration, security SecurityLevel, proxyFor upspin.Endpoint) (*AuthClientService, error) {
	const op = "auth/grpcauth.NewGRPCClient"
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
	ac := &AuthClientService{
		context:           context,
		keepAliveInterval: keepAliveInterval,
		closeKeepAlive:    make(chan bool, 1),
		proxyFor:          proxyFor,
	}
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
func (ac *AuthClientService) keepAlive() {
	sleepFor := ac.keepAliveInterval
	for {
		select {
		case <-time.After(sleepFor):
			lastIdleness := time.Since(ac.lastActivity())
			if lastIdleness < ac.keepAliveInterval {
				sleepFor = ac.keepAliveInterval - lastIdleness
				log.Debug.Printf("New ping in %v", sleepFor)
				continue
			}
			sleepFor = ac.keepAliveInterval
			if !ac.Ping() {
				log.Error.Printf("grpcauth: keepAlive: ping failed")
			}
			log.Debug.Printf("grpcAuth: keepAlive: ping okay")
			ac.setLastActivity()
		case <-ac.closeKeepAlive:
			log.Debug.Printf("grpcauth: keepAlive: exiting keep alive routine")
			return
		}
	}
}

// lastActivity reports the time of the last known network activity.
func (ac *AuthClientService) lastActivity() time.Time {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.lastNetActivity
}

// setLastActivity sets the current time as the last known network acitivity. This is useful
// when using application pings, to prevent unnecessarily frequent pings.
func (ac *AuthClientService) setLastActivity() {
	ac.mu.Lock()
	ac.lastNetActivity = time.Now()
	ac.mu.Unlock()
}

// SetService sets the underlying RPC service which was obtained with proto.NewSERVICENAMEClient, where SERVICENAME is
// the RPC service definition from the proto file.
func (ac *AuthClientService) SetService(common GRPCCommon) {
	ac.grpcCommon = common
}

// GRPCConn returns the grpc client connection used to dial the server.
func (ac *AuthClientService) GRPCConn() *grpc.ClientConn {
	return ac.grpcConn
}

func (ac *AuthClientService) dialWithKeepAlive(target string, timeout time.Duration) (net.Conn, error) {
	// Invalidate auth token and mark proxy as not configured.
	ac.mu.Lock()
	ac.authToken = ""
	ac.proxyConfigured = false
	ac.mu.Unlock()

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
func (ac *AuthClientService) Ping() bool {
	seq := rand.Int31()
	req := &proto.PingRequest{
		PingSequence: seq,
	}
	gctx, cancel := gContext.WithTimeout(gContext.Background(), 3*time.Second)
	defer cancel()
	resp, err := ac.grpcCommon.Ping(gctx, req)
	if err != nil {
		log.Printf("Ping error: %s", err)
	}
	ac.setLastActivity()
	return err == nil && resp.PingSequence == seq
}

func (ac *AuthClientService) isAuthTokenExpired() bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.authToken == "" || ac.lastTokenRefresh.Add(tokenFreshnessDuration).Before(time.Now())
}

func (ac *AuthClientService) isProxy() bool {
	return ac.proxyFor.Transport != upspin.Unassigned
}

func (ac *AuthClientService) proxyNeedsConfiguration() bool {
	if !ac.isProxy() {
		return false
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return !ac.proxyConfigured
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
// 	resp, err := c.grpcClient.DoATrump(ctx, req, callOpt)
// 	err = finishAuth(err)
// 	// handle err
func (ac *AuthClientService) NewAuthContext() (ctx gContext.Context, opt grpc.CallOption, finishAuth func(error) error, err error) {
	const op = "auth/grpcauth.AuthClientService"

	ctx = gContext.Background()

	var header metadata.MD
	opt = grpc.Header(&header)

	if !ac.isAuthTokenExpired() {
		ac.mu.Lock()
		token := ac.authToken
		ac.mu.Unlock()
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
	if ac.proxyNeedsConfiguration() {
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
			return errors.E(op, errors.Str("no auth token in response header"))
		}
		now := time.Now()

		// If talking to a proxy, make sure it is running as the same user.
		if ac.isProxy() {
			msg, ok := header[authRequestKey]
			if !ok {
				return errors.E(op, errors.Str("proxy server must authenticate"))
			}
			if err := ac.verifyServerUser(msg); err != nil {
				log.Error.Printf("%s: client can't verify server user: %s", op, err)
				return errors.E(op, err)
			}
		}

		ac.mu.Lock()
		defer ac.mu.Unlock()
		ac.authToken = token[0]
		ac.lastTokenRefresh = now
		ac.proxyConfigured = ac.isProxy()
		return nil
	}
	return
}

// verifyServerUser ensures server is running as the same user. It assumes that msg[0] is
// the user name.
func (ac *AuthClientService) verifyServerUser(msg []string) error {
	u := upspin.UserName(msg[0])
	if ac.context.UserName() != u {
		return errors.Errorf("client %s does not match server %s", ac.context.UserName(), u)
	}

	// Get user's public key.
	keyServer, err := bind.KeyServer(ac.context, ac.context.KeyEndpoint())
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

// Close implements upspin.Service.
func (ac *AuthClientService) Close() {
	select { // prevents blocking if Close is called more than once.
	case ac.closeKeepAlive <- true:
		close(ac.closeKeepAlive)
	default:
	}
	// The only error returned is ErrClientConnClosing, meaning something else has already caused it to close.
	_ = ac.grpcConn.Close() // explicitly ignore the error as there's nothing we can do.
}
