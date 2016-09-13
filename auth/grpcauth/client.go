// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package grpcauth

import (
	"crypto/tls"
	"fmt"
	"math/big"
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
	// Configure is the GRPC call for Configure.
	Configure(ctx gContext.Context, in *proto.ConfigureRequest, opts ...grpc.CallOption) (*proto.ConfigureResponse, error)
}

// AuthClientService is a partial Service that uses GRPC as transport and implements Authentication.
type AuthClientService struct {
	grpcCommon GRPCCommon
	grpcConn   *grpc.ClientConn
	context    upspin.Context
	netAddr    upspin.NetAddr

	keepAliveInterval time.Duration // interval of keep-alive packets.
	closeKeepAlive    chan bool     // channel used to tell the keep-alive routine to exit.

	mu               sync.Mutex // protects the field below.
	authToken        string
	lastTokenRefresh time.Time
	lastNetActivity  time.Time // last known time of some network activity.
}

// SecurityLevel defines the security required of a GRPC connection.
type SecurityLevel int

const (
	// Secure as the security argument to NewGRPCClient requires TLS connections using CA certificates.
	Secure = SecurityLevel(iota + 1)

	// InsecureAllowingSelfSignedCertificates as the security argument to NewGRPCClient requires TLS connections
	// but allows self signed certificates.
	InsecureAllowingSelfSignedCertificates

	// NoSecurity as the security argument to NewGRPCClient requires connections with no authentication or encryption.
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
func NewGRPCClient(context upspin.Context, netAddr upspin.NetAddr, keepAliveInterval time.Duration, security SecurityLevel) (*AuthClientService, error) {
	op := opf("NewGRPCClient", "%q", netAddr)

	if keepAliveInterval != 0 && keepAliveInterval < time.Minute {
		log.Info.Printf("auth/grpcauth: keepAliveInteral too short (%v)", keepAliveInterval)
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
	opts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithDialer(dialWithKeepAlive),
		grpc.WithTimeout(3 * time.Second),
	}
	switch security {
	case NoSecurity:
		// Only allow insecure connections to the loop back network.
		if !isLocal(addr[skip:]) {
			return nil, op.error(errors.IO, errors.Errorf("insecure dial to non-loopback destination: %v", netAddr))
		}
		opts = append(opts, grpc.WithInsecure())
	case Secure:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: false})))
	case InsecureAllowingSelfSignedCertificates:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})))
	default:
		return nil, op.error(errors.Invalid, errors.Errorf("invalid security level to NewGRPCClient: %v", security))
	}
	conn, err := grpc.Dial(addr[skip:], opts...)
	if err != nil {
		return nil, op.error(err)
	}
	ac := &AuthClientService{
		grpcConn:          conn,
		context:           context,
		netAddr:           netAddr,
		keepAliveInterval: keepAliveInterval,
		closeKeepAlive:    make(chan bool, 1),
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
			lastIdleness := time.Since(ac.LastActivity())
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
			ac.SetLastActivity()
		case <-ac.closeKeepAlive:
			log.Debug.Printf("grpcauth: keepAlive: exiting keep alive routine")
			return
		}
	}
}

// LastActivity reports the time of the last known network activity.
func (ac *AuthClientService) LastActivity() time.Time {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.lastNetActivity
}

// SetLastActivity sets the current time as the last known network acitivity. This is useful
// when using application pings, to prevent unnecessarily frequent pings.
func (ac *AuthClientService) SetLastActivity() {
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

func dialWithKeepAlive(target string, timeout time.Duration) (net.Conn, error) {
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
	op := opf("Ping", "%q", ac.netAddr)

	seq := rand.Int31()
	req := &proto.PingRequest{
		PingSequence: seq,
	}
	gctx, cancel := gContext.WithTimeout(gContext.Background(), 3*time.Second)
	defer cancel()
	resp, err := ac.grpcCommon.Ping(gctx, req)
	if err != nil {
		op.error(err)
	}
	ac.SetLastActivity()
	return err == nil && resp.PingSequence == seq
}

func (ac *AuthClientService) isAuthTokenExpired() bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.authToken == "" || ac.lastTokenRefresh.Add(tokenFreshnessDuration).Before(time.Now())
}

// NewAuthContext sets up a Context, CallOption, and validate function for
// authenticating GRPC requests. If a request token is available, it puts
// that token in the context as GRPC metadata. If the request token
// is not available or has expired, it puts authentication request data
// in the context, and sets up a GRPC Call Option and validate function
// for retrieving the authentication response from the GRPC response headers.
//
// Example usage:
// 	ctx, callOpt, validate, err := ac.NewAuthContext()
// 	// handle err
// 	req := &proto.RequestMessage{ ... }
// 	resp, err := c.grpcClient.DoATrump(ctx, req, callOpt)
// 	// handle err
// 	if err := validate(); err != nil {
// 		// handle err
// 	}
func (ac *AuthClientService) NewAuthContext() (ctx gContext.Context, opt grpc.CallOption, validate func() error, err error) {
	op := opf("NewAuthContext", "%q, %q", ac.netAddr)

	ctx = gContext.Background()

	var header metadata.MD
	opt = grpc.Header(&header)

	if !ac.isAuthTokenExpired() {
		ac.mu.Lock()
		token := ac.authToken
		ac.mu.Unlock()
		ctx = metadata.NewContext(ctx, metadata.Pairs(authTokenKey, token))
		validate = func() error {
			ac.SetLastActivity()
			return nil
		}
		return
	}

	user := string(ac.context.UserName())
	now := time.Now().UTC().Format(time.ANSIC) // to discourage signature replay
	sig, err := ac.context.Factotum().UserSign([]byte(user + " Authenticate " + now))
	if err != nil {
		return nil, nil, nil, op.error(err)
	}
	ctx = metadata.NewContext(ctx, metadata.MD{authRequestKey: []string{
		user,
		now,
		sig.R.String(),
		sig.S.String(),
	}})
	validate = func() error {
		token, ok := header[authTokenKey]
		if !ok || len(token) != 1 {
			return errors.Str("no auth token in response header")
		}
		now := time.Now()

		ac.mu.Lock()
		defer ac.mu.Unlock()
		ac.authToken = token[0]
		ac.lastTokenRefresh = now
		ac.lastNetActivity = now
		return nil
	}
	return
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

// ConfigureProxy uses the Configure command to tell the proxy the endpoint it is proxying for and
// to ensure that the proxy is running as our upspin user identity.
func (ac *AuthClientService) ConfigureProxy(ctx upspin.Context, e upspin.Endpoint) (upspin.UserName, error) {
	op := opf("ConfigureProxy", "%q, %q, %q", ac.netAddr, ctx.UserName(), e)

	gCtx, callOpt, validate, err := ac.NewAuthContext()
	if err != nil {
		return "", op.error(err)
	}

	token, err := generateRandomToken()
	if err != nil {
		return "", op.error(err)
	}
	req := &proto.ConfigureRequest{
		Options: []string{"authenticate=" + token, "endpoint=" + e.String()},
	}
	resp, err := ac.grpcCommon.Configure(gCtx, req, callOpt)
	if err != nil {
		return "", op.error(err)
	}
	if err := validate(); err != nil {
		return "", op.error(err)
	}

	// Get user's public keys.
	keyServer, err := bind.KeyServer(ctx, ctx.KeyEndpoint())
	if err != nil {
		return "", op.error(err)
	}
	u, err := keyServer.Lookup(upspin.UserName(resp.UserName))
	if err != nil {
		return "", op.error(err)
	}
	key := u.PublicKey

	// Parse signature
	var rs, ss big.Int
	_, ok := rs.SetString(resp.Signature.R, 10)
	if !ok {
		return "", op.error(errors.Str("bad signature"))
	}
	_, ok = ss.SetString(resp.Signature.S, 10)
	if !ok {
		return "", op.error(errors.Str("bad signature"))
	}

	// Validate signature.
	err = verifySignature(key, []byte(resp.UserName+" Authenticate "+token), &rs, &ss)
	if err != nil {
		return "", op.error(err)
	}
	return upspin.UserName(resp.UserName), nil
}

func opf(method string, format string, args ...interface{}) *operation {
	op := &operation{"auth/grpcauth." + method, fmt.Sprintf(format, args...)}
	log.Debug.Print(op)
	return op
}

type operation struct {
	op   string
	args string
}

func (op *operation) String() string {
	return fmt.Sprintf("%s(%s)", op.op, op.args)
}

func (op *operation) error(args ...interface{}) error {
	if len(args) == 0 {
		panic("error called with zero args")
	}
	if len(args) == 1 {
		if e, ok := args[0].(error); ok && e == upspin.ErrFollowLink {
			return e
		}
		if args[0] == nil {
			return nil
		}
	}
	log.Debug.Printf("%v error: %v", op, errors.E(args...))
	return errors.E(append([]interface{}{op.op}, args...)...)
}
